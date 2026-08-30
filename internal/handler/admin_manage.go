package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type AdminManage struct {
	Products  *repo.Products
	Users     *repo.Users
	Servers   *repo.Servers
	Balance   *repo.Balance
	Svc       *service.ServicesRepo
	Lifecycle *service.Lifecycle
	Payment   *service.Payment
	Providers *server.Registry
	Settings  *repo.Settings
}

func (m *AdminManage) require(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

// audit 记录后台操作审计（失败仅记日志，不阻断主流程）。
func (m *AdminManage) audit(r *http.Request, action, targetType string, targetID int64, detail string) {
	var aid int64
	if s := middleware.FromSession(r.Context()); s != nil {
		aid = s.UserID
	}
	repo.RecordAudit(m.Users.DB, aid, action, targetType, targetID, detail, r.RemoteAddr)
}

// ---------- 分类管理 ----------

// typeRow 分类树行（两级，模板用）。
type typeRow struct {
	ID           int64
	ParentID     int64
	Name         string
	Description  string
	Sort         int
	Hidden       bool
	ProductCount int
	Children     []typeRow
}

// buildTypeRows 扁平分类组装为两级树（一级 + Children），并挂直挂产品数。
func buildTypeRows(types []repo.ProductType, counts map[int64]int) []typeRow {
	var firsts []typeRow
	idx := map[int64]int{}
	for _, t := range types {
		if t.ParentID == 0 {
			idx[t.ID] = len(firsts)
			firsts = append(firsts, typeRow{
				ID: t.ID, ParentID: 0, Name: t.Name, Description: t.Description,
				Sort: t.Sort, Hidden: t.Hidden, ProductCount: counts[t.ID],
			})
		}
	}
	for _, t := range types {
		if t.ParentID == 0 {
			continue
		}
		if i, ok := idx[t.ParentID]; ok {
			firsts[i].Children = append(firsts[i].Children, typeRow{
				ID: t.ID, ParentID: t.ParentID, Name: t.Name, Description: t.Description,
				Sort: t.Sort, Hidden: t.Hidden, ProductCount: counts[t.ID],
			})
		}
	}
	return firsts
}

func (m *AdminManage) TypesList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	list, err := m.Products.ListTypes(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	counts, _ := m.Products.TypeProductCounts(r.Context())
	renderAdmin(w, "admin_types.html", AdminData{
		Rows: buildTypeRows(list, counts), CSRF: csrfOf(adminSessions, w, r), Error: r.URL.Query().Get("err"),
	})
}

// typeRedirect 分类错误跳转（err 统一转义，防中文消息破链接）。
func typeRedirect(w http.ResponseWriter, r *http.Request, err error) {
	http.Redirect(w, r, "/admin/types?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
}

func (m *AdminManage) TypeSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		typeRedirect(w, r, errors.New("名称必填"))
		return
	}
	desc := strings.TrimSpace(r.PostFormValue("description"))
	sort, _ := strconv.Atoi(r.PostFormValue("sort"))
	parentID, _ := strconv.ParseInt(r.PostFormValue("parent_id"), 10, 64)
	hidden := r.PostFormValue("hidden") != ""
	var id int64
	if idStr := r.PostFormValue("id"); idStr != "" {
		id, _ = strconv.ParseInt(idStr, 10, 64)
	}
	// 父分类校验：必须存在且为一级，且不能是自己（防自环）
	if parentID != 0 {
		types, err := m.Products.ListTypes(r.Context())
		if err != nil {
			typeRedirect(w, r, errors.New("查询失败"))
			return
		}
		parent, found := repo.FindType(types, parentID)
		if !found {
			typeRedirect(w, r, repo.ErrTypeNotFound)
			return
		}
		if parent.ParentID != 0 {
			typeRedirect(w, r, errors.New("仅支持两级分类，父分类必须为一级分类"))
			return
		}
		if parentID == id {
			typeRedirect(w, r, errors.New("不能将自己设为父分类"))
			return
		}
	}
	var err error
	if id == 0 {
		_, err = m.Products.CreateType(r.Context(), name, desc, sort, parentID, hidden)
	} else {
		err = m.Products.UpdateType(r.Context(), id, name, desc, sort, parentID, hidden)
	}
	if err != nil {
		typeRedirect(w, r, err)
		return
	}
	if id == 0 {
		m.audit(r, "type_create", "type", parentID, name)
	} else {
		m.audit(r, "type_update", "type", id, name)
	}
	http.Redirect(w, r, "/admin/types", http.StatusSeeOther)
}

// TypeMoveProducts 整组移动产品到其他分类（清空后才能删除，对齐 ZJMF）。
func (m *AdminManage) TypeMoveProducts(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	from, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	to, _ := strconv.ParseInt(r.PostFormValue("target_id"), 10, 64)
	types, err := m.Products.ListTypes(r.Context())
	if err != nil {
		typeRedirect(w, r, errors.New("查询失败"))
		return
	}
	if _, ok := repo.FindType(types, from); !ok {
		typeRedirect(w, r, repo.ErrTypeNotFound)
		return
	}
	if target, ok := repo.FindType(types, to); !ok || target.ParentID == 0 || from == to {
		typeRedirect(w, r, errors.New("目标分类无效"))
		return
	}
	n, err := m.Products.MoveTypeProducts(r.Context(), from, to)
	if err != nil {
		typeRedirect(w, r, err)
		return
	}
	m.audit(r, "type_move_products", "type", from, fmt.Sprintf("移动 %d 个产品到分类 %d", n, to))
	http.Redirect(w, r, "/admin/types?msg="+url.QueryEscape(fmt.Sprintf("已移动 %d 个产品", n)), http.StatusSeeOther)
}

func (m *AdminManage) TypeDelete(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := m.Products.DeleteType(r.Context(), id); err != nil {
		typeRedirect(w, r, err)
		return
	}
	m.audit(r, "type_delete", "type", id, "")
	http.Redirect(w, r, "/admin/types", http.StatusSeeOther)
}

func (m *AdminManage) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return false
	}
	return true
}

func checkCSRF(r *http.Request, tok string) bool {
	sess := middleware.FromSession(r.Context())
	return sess != nil && sess.CSRFToken() == tok
}

// ---------- 产品管理 ----------

type adminProductRow struct {
	ID      int64
	A, B, C string // 名称 / 分类 / 月付价
	D       string // 显示状态
}

func (m *AdminManage) ProductsList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	list, err := m.Products.ListAll(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	types, _ := m.Products.ListTypes(r.Context())
	// 分类列显示完整路径：一级/二级
	typeName := map[int64]string{}
	parentName := map[int64]string{}
	for _, t := range types {
		if t.ParentID == 0 {
			parentName[t.ID] = t.Name
		}
	}
	for _, t := range types {
		if p, ok := parentName[t.ParentID]; ok {
			typeName[t.ID] = p + "/" + t.Name
		} else {
			typeName[t.ID] = t.Name
		}
	}
	psID, _ := m.Products.DefaultPricesetID(r.Context())
	rows := make([]adminProductRow, 0, len(list))
	for _, p := range list {
		mn := "-"
		if pr, err := m.Products.Price(r.Context(), p.ID, psID); err == nil {
			opts, _ := m.Products.GetConfigOptions(r.Context(), p.ID)
			eType, eVal := resolveProfitType(r.Context(), p.ProfitType, p.ProfitValue, m.Products.DB, p.ID), resolveProfitValue(r.Context(), p.ProfitType, p.ProfitValue, m.Products.DB, p.ID)
			mn = fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(pr.Monthly), opts, eType, eVal))
		}
		h := "显示"
		if p.Hidden {
			h = "隐藏"
		}
		rows = append(rows, adminProductRow{
			ID: p.ID, A: p.Name, B: typeName[p.TypeID.Int64], C: mn, D: h,
		})
	}
	renderAdmin(w, "admin_products.html", AdminData{
		Rows: rows, CSRF: csrfOf(adminSessions, w, r), Error: r.URL.Query().Get("err"),
		Msg:               r.URL.Query().Get("msg"),
		GlobalProfitType:  loadGlobalProfitType(r.Context(), m.Settings),
		GlobalProfitValue: loadGlobalProfitValue(r.Context(), m.Settings),
	})
}

func (m *AdminManage) ProductForm(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	types, _ := m.Products.ListTypes(r.Context())
	data := AdminData{Types: types, CSRF: csrfOf(adminSessions, w, r)}
	if pid := r.PathValue("id"); pid != "" {
		id, _ := strconv.ParseInt(pid, 10, 64)
		p, err := m.Products.Get(r.Context(), id)
		if err != nil {
			// 区分“产品不存在(404)”与“查询出错(500+日志)”，避免真实错误被吞成 404 难以排查。
			if !errors.Is(err, repo.ErrNotFound) {
				log.Printf("[admin] 产品查询失败 id=%d: %v", id, err)
				http.Error(w, "查询失败", http.StatusInternalServerError)
				return
			}
			http.NotFound(w, r)
			return
		}
		data.Product = p
		// 按需同步上游价格与库存（编辑产品时拉取最新）
		m.syncProductUpstream(r.Context(), p)
		psID, _ := m.Products.DefaultPricesetID(r.Context())
		if pr, err := m.Products.Price(r.Context(), id, psID); err == nil {
			data.Monthly, data.Quarterly, data.Yearly = pr.Monthly, pr.Quarterly, pr.Yearly
		}
		if opts, err := m.Products.GetConfigOptions(r.Context(), id); err == nil && len(opts) > 0 {
			if b, mErr := json.MarshalIndent(opts, "", "  "); mErr == nil {
				data.ConfigJSON = string(b)
			}
		}
	}
	serversList, _ := m.Servers.List(r.Context())
	data.ServersList = serversList
	// 供应商产品表单差异声明（模板动态适配，新上游零模板改动）
	if b, err := json.Marshal(m.Providers.ProductFormHintsSets()); err == nil {
		data.ProductHintsJSON = template.JS(b)
	}
	// 各供应商产品表单独立区块（插槽注入，同详情页 DetailWidget 模式）
	var wf strings.Builder
	for _, pi := range m.Providers.List() {
		prov, err := m.Providers.Get(pi.Code)
		if err != nil {
			continue
		}
		wp, ok := prov.(server.ProductFormWidgetProvider)
		if !ok {
			continue
		}
		html, err := wp.ProductFormWidget()
		if err != nil {
			log.Printf("[admin] %s 产品表单区块渲染失败: %v", pi.Code, err)
			continue
		}
		wf.WriteString(`<div class="provform" data-provider="` + pi.Code + `" style="display:none">`)
		wf.Write([]byte(html))
		wf.WriteString(`</div>`)
	}
	data.ProviderWidgets = template.HTML(wf.String())
	if p, ok := data.Product.(*repo.Product); ok {
		data.UpstreamBound = p.ServerID.Valid && p.UpstreamPID > 0
	}
	renderAdmin(w, "admin_product_form.html", data)
}

// PullConfig POST /admin/products/{id}/pull-config — 从上游拉配置项写入本地。
func (m *AdminManage) PullConfig(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := m.Products.Get(r.Context(), id)
	if err != nil || !p.ServerID.Valid || p.UpstreamPID == 0 {
		http.Error(w, "该产品未绑定上游", http.StatusBadRequest)
		return
	}
	sv, err := m.Servers.Get(r.Context(), p.ServerID.Int64)
	if err != nil {
		http.Error(w, "读取服务器失败", http.StatusInternalServerError)
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": err.Error()})
		return
	}
	fetcher, ok := prov.(server.ConfigOptionsFetcher)
	if !ok {
		writeJSON(w, map[string]string{"ok": "0", "msg": "该供应商不支持配置项拉取"})
		return
	}
	opts, err := fetcher.FetchProductConfigOptions(r.Context(), serverConfig(sv), p.UpstreamPID)
	if err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": err.Error()})
		return
	}
	if err := m.Products.SaveConfigOptions(r.Context(), id, opts); err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": "保存失败: " + err.Error()})
		return
	}
	// 同步刷新上游基础价（基础价格常被漏抓，需在此一并更新 product_prices）。
	price := map[string]float64{"monthly": 0, "quarterly": 0, "yearly": 0}
	if pf, ok := prov.(server.PriceFetcher); ok {
		if pm, pq, py, perr := pf.FetchProductPrice(r.Context(), serverConfig(sv), p.UpstreamPID); perr == nil {
			price["monthly"], price["quarterly"], price["yearly"] = pm, pq, py
			if psID, e2 := m.Products.DefaultPricesetID(r.Context()); e2 == nil {
				_, _ = m.Products.DB.ExecContext(r.Context(),
					`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,$3,$4,$5)
					 ON CONFLICT (product_id,priceset_id) DO UPDATE SET monthly=$3,quarterly=$4,yearly=$5`,
					id, psID, money(pm), money(pq), money(py))
			}
		}
	}
	b, _ := json.MarshalIndent(opts, "", "  ")
	writeJSON(w, map[string]any{"ok": "1", "count": len(opts), "json": string(b), "price": price})
}

func (m *AdminManage) ProductSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/admin/products?err=名称必填", http.StatusSeeOther)
		return
	}
	desc := strings.TrimSpace(r.PostFormValue("description"))
	stock, _ := strconv.Atoi(r.PostFormValue("stock"))
	hidden := r.PostFormValue("hidden") == "1"
	var typeID sql.NullInt64
	if v := r.PostFormValue("type_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		typeID = sql.NullInt64{Int64: id, Valid: true}
	}
	if !typeID.Valid {
		http.Redirect(w, r, "/admin/products?err="+url.QueryEscape("请选择二级分类"), http.StatusSeeOther)
		return
	}
	types, terr := m.Products.ListTypes(r.Context())
	if terr != nil {
		http.Redirect(w, r, "/admin/products?err="+url.QueryEscape("分类查询失败"), http.StatusSeeOther)
		return
	}
	if t, ok := repo.FindType(types, typeID.Int64); !ok || t.ParentID == 0 {
		http.Redirect(w, r, "/admin/products?err="+url.QueryEscape("商品只能挂在二级分类下"), http.StatusSeeOther)
		return
	}
	psID, _ := m.Products.DefaultPricesetID(r.Context())
	monthly := normalizeAmount(r.PostFormValue("monthly"))
	quarterly := normalizeAmount(r.PostFormValue("quarterly"))
	yearly := normalizeAmount(r.PostFormValue("yearly"))
	configJSON := strings.TrimSpace(r.PostFormValue("configoption"))
	if configJSON != "" {
		var validate []repo.ConfigOption
		if err := json.Unmarshal([]byte(configJSON), &validate); err != nil {
			http.Redirect(w, r, "/admin/products?err=配置项 JSON 格式错误: "+err.Error(), http.StatusSeeOther)
			return
		}
	}
	// 上游绑定
	var bindServer sql.NullInt64
	if v := r.PostFormValue("server_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		bindServer = sql.NullInt64{Int64: id, Valid: true}
	}
	upstreamPID, _ := strconv.ParseInt(r.PostFormValue("upstream_pid"), 10, 64)
	// 利润（对齐 ZJMF 上游利润）：0百分比 1固定金额
	profitType, _ := strconv.ParseInt(r.PostFormValue("profit_type"), 10, 64)
	if profitType != 1 {
		profitType = 0
	}
	profitValue, _ := strconv.ParseFloat(r.PostFormValue("profit_value"), 64)
	if profitValue < 0 {
		profitValue = 0
	}
	// 无上游成本的供应商（如 EasyPanel）：利润加成无意义，服务端强制归零（表单已按类型隐藏）
	if bindServer.Valid {
		if sv, serr := m.Servers.Get(r.Context(), bindServer.Int64); serr == nil {
			if prov, perr := m.Providers.Get(sv.Provider); perr == nil {
				if mf, ok := prov.(server.MarkupFreeProvider); ok && mf.MarkupFree() {
					profitType, profitValue = 0, 0
				}
			}
		}
	}
	savePrice := func(pid int64) error {
		_, err := m.Products.DB.ExecContext(r.Context(),
			`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT (product_id,priceset_id) DO UPDATE SET monthly=$3,quarterly=$4,yearly=$5`,
			pid, psID, monthly, quarterly, yearly)
		return err
	}
	if idStr := r.PostFormValue("id"); idStr == "" {
		pid, err := m.Products.Create(r.Context(), typeID, name, desc, stock)
		if err == nil && configJSON != "" {
			err = m.Products.SaveConfigOptions(r.Context(), pid, mustOpts(configJSON))
		}
		if err == nil && bindServer.Valid {
			err = m.Products.SetBinding(r.Context(), pid, bindServer, upstreamPID)
		}
		if err == nil {
			err = savePrice(pid)
		}
		if err == nil {
			err = m.Products.SetProfit(r.Context(), pid, int16(profitType), profitValue)
		}
		if err != nil {
			http.Redirect(w, r, "/admin/products?err="+err.Error(), http.StatusSeeOther)
			return
		}
		m.audit(r, "product_create", "product", pid, name)
	} else {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		err := m.Products.Update(r.Context(), id, typeID, name, desc, stock, hidden)
		if err == nil {
			err = m.Products.SetBinding(r.Context(), id, bindServer, upstreamPID)
		}
		if err == nil {
			err = m.Products.SaveConfigOptions(r.Context(), id, mustOpts(configJSON)) // 空串清空配置
		}
		if err == nil {
			err = savePrice(id)
		}
		if err == nil {
			err = m.Products.SetProfit(r.Context(), id, int16(profitType), profitValue)
		}
		if err != nil {
			http.Redirect(w, r, "/admin/products?err="+err.Error(), http.StatusSeeOther)
			return
		}
		m.audit(r, "product_update", "product", id, name)
	}
	http.Redirect(w, r, "/admin/products", http.StatusSeeOther)
}

func (m *AdminManage) ProductDelete(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	m.Products.Delete(r.Context(), id)
	m.audit(r, "product_delete", "product", id, "")
	http.Redirect(w, r, "/admin/products", http.StatusSeeOther)
}

// ---------- 用户列表 ----------

func (m *AdminManage) UsersList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	list, err := m.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	var rows []adminRow
	for _, u := range list {
		rows = append(rows, adminRow{ID: u.ID, A: u.Email, B: u.Name})
	}
	renderAdmin(w, "admin_users.html", AdminData{Rows: rows})
}

func normalizeAmount(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0.00"
	}
	return s
}

func mustOpts(s string) []repo.ConfigOption {
	var opts []repo.ConfigOption
	json.Unmarshal([]byte(s), &opts) // 已在 save 前校验过
	return opts
}

// ---------- 上游目录浏览与导入（挂在 AdminManage，复用 Products/Users repo） ----------

// CatalogPage GET /admin/servers/{id}/catalog — 浏览上游商品目录。
func (m *AdminManage) CatalogPage(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sv, err := m.Servers.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		http.Error(w, "供应商错误", http.StatusInternalServerError)
		return
	}
	list, err := prov.Catalog(ctx, serverConfig(sv))
	linked, lerr := linkedUpstreamPIDs(ctx, m.Products.DB, id)
	if lerr != nil {
		log.Printf("[catalog] 查询已对接商品失败: %v", lerr)
		linked = map[int]bool{}
	}
	// 一级分类清单：供导入时选择“上游分组建为某分类下的二级”
	var firstTypes []repo.ProductType
	if types, terr := m.Products.ListTypes(r.Context()); terr == nil {
		for _, t := range types {
			if t.ParentID == 0 {
				firstTypes = append(firstTypes, t)
			}
		}
	}
	renderAdmin(w, "admin_catalog.html", AdminData{
		CSRF: csrfOf(adminSessions, w, r),
		// 目录拉取错误优先；否则回显导入跳转携带的错误
		Error: func() string {
			if e := catalogErr(err); e != "" {
				return e
			}
			return r.URL.Query().Get("err")
		}(),
		ServersList: sv,
		Rows:        toCatalogRows(list, linked),
		Types:       firstTypes,
		// 目录导入利润独立于服务器默认利润，默认 0/0，不自动带入服务器配置。
		ServerProfitType: 0, ServerProfitValue: 0,
	})
}

func catalogErr(err error) string {
	if err == nil {
		return ""
	}
	return "拉取目录失败: " + err.Error()
}

type catalogRow struct {
	PID     int64
	A, B, C string // 名称 / 分组 / 月付价
	Stock   string
	Linked  bool // 是否已对接为本地产品
}

func toCatalogRows(list []server.UpstreamProduct, linked map[int]bool) []catalogRow {
	rows := make([]catalogRow, 0, len(list))
	for _, u := range list {
		st := "不限"
		if u.Stock >= 0 {
			st = itoa(int64(u.Stock))
		}
		rows = append(rows, catalogRow{
			PID: int64(u.PID), A: u.Name, B: u.GroupName,
			C: fmt.Sprintf("%.2f", u.DisplayPrice()), Stock: st, Linked: linked[u.PID],
		})
	}
	return rows
}

// UpstreamOptions GET /admin/products/upstream-options — 返回某上游服务器商品目录（按分组），
// 供产品表单级联下拉选品，避免手填 PID。
func (m *AdminManage) UpstreamOptions(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	serverID, _ := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	if serverID <= 0 {
		writeJSON(w, map[string]any{"ok": 0, "msg": "server_id 无效"})
		return
	}
	sv, err := m.Servers.Get(r.Context(), serverID)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "读取服务器失败"})
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var list []server.UpstreamProduct
	if cl, ok := prov.(server.CatalogLister); ok {
		list, err = cl.CatalogLight(ctx, serverConfig(sv))
	} else {
		list, err = prov.Catalog(ctx, serverConfig(sv))
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "拉取目录失败: " + err.Error()})
		return
	}
	type item struct {
		PID  int64  `json:"pid"`
		Name string `json:"name"`
	}
	type group struct {
		Name  string `json:"name"`
		Items []item `json:"items"`
	}
	groups := map[string]*group{}
	order := make([]string, 0)
	for _, u := range list {
		g, ok := groups[u.GroupName]
		if !ok {
			g = &group{Name: u.GroupName}
			groups[u.GroupName] = g
			order = append(order, u.GroupName)
		}
		g.Items = append(g.Items, item{PID: int64(u.PID), Name: u.Name})
	}
	out := make([]group, 0, len(order))
	for _, n := range order {
		out = append(out, *groups[n])
	}
	writeJSON(w, map[string]any{"ok": 1, "groups": out})
}

// UpstreamConfig GET /admin/products/upstream-config — 直接拉取上游商品配置项（无需本地产品），
// 供表单选品时自动填充配置项文本框，去掉"先保存再拉取"的割裂。
func (m *AdminManage) UpstreamConfig(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	serverID, _ := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	pid, _ := strconv.Atoi(r.URL.Query().Get("pid"))
	if serverID <= 0 || pid <= 0 {
		writeJSON(w, map[string]any{"ok": 0, "msg": "参数无效"})
		return
	}
	sv, err := m.Servers.Get(r.Context(), serverID)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "读取服务器失败"})
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	fetcher, ok := prov.(server.ConfigOptionsFetcher)
	if !ok {
		writeJSON(w, map[string]any{"ok": 0, "msg": "该供应商不支持配置项拉取"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	opts, err := fetcher.FetchProductConfigOptions(ctx, serverConfig(sv), int64(pid))
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	// 同时返回上游基础价，供前端刷新月/季/年输入框（基础价常被漏抓）。
	price := map[string]float64{"monthly": 0, "quarterly": 0, "yearly": 0}
	if pf, ok := prov.(server.PriceFetcher); ok {
		if pm, pq, py, perr := pf.FetchProductPrice(ctx, serverConfig(sv), int64(pid)); perr == nil {
			price["monthly"], price["quarterly"], price["yearly"] = pm, pq, py
		}
	}
	// 同时返回上游描述与库存，供新建页自动填充（与导入保持一致）。
	desc := ""
	stock := -1
	if mf, ok := prov.(server.ProductMetaFetcher); ok {
		if d, st, merr := mf.FetchProductMeta(ctx, serverConfig(sv), int64(pid)); merr == nil {
			desc = cleanDesc(d)
			stock = st
		}
	}
	b, _ := json.MarshalIndent(opts, "", "  ")
	writeJSON(w, map[string]any{"ok": 1, "count": len(opts), "json": string(b), "price": price, "description": desc, "stock": stock})
}

// ImportProducts POST /admin/servers/{id}/import — 勾选导入上游商品为本地产品。
func (m *AdminManage) ImportProducts(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	serverID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sv, err := m.Servers.Get(r.Context(), serverID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	// 导入利润：表单未填时回退服务器默认利润
	// 目录导入默认 0/0；只使用本次表单提交值，不回退服务器利润配置。
	profitType, _ := strconv.ParseInt(r.PostFormValue("profit_type"), 10, 64)
	if profitType != 1 {
		profitType = 0
	}
	profitValue, _ := strconv.ParseFloat(r.PostFormValue("profit_value"), 64)
	if profitValue < 0 {
		profitValue = 0
	}
	// 导入目标父分类：0=按上游分组自动建；>0=上游分组建为该一级分类下的二级
	parentID, _ := strconv.ParseInt(r.PostFormValue("parent_id"), 10, 64)
	if parentID <= 0 {
		http.Redirect(w, r, fmt.Sprintf("/admin/servers/%d/catalog?err=%s", serverID,
			url.QueryEscape("请选择导入目标一级分类")), http.StatusSeeOther)
		return
	}
	if parentID != 0 {
		types, terr := m.Products.ListTypes(r.Context())
		t, ok := repo.FindType(types, parentID)
		if terr != nil || !ok || t.ParentID != 0 {
			http.Redirect(w, r, fmt.Sprintf("/admin/servers/%d/catalog?err=%s", serverID,
				url.QueryEscape("导入目标分类无效（需为一级分类）")), http.StatusSeeOther)
			return
		}
	}
	imported := 0
	for _, pidStr := range r.PostForm["import"] {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}
		if m.importUpstreamProduct(r.Context(), sv, serverID, pid, int16(profitType), profitValue, parentID) {
			imported++
		}
	}
	http.Redirect(w, r,
		fmt.Sprintf("/admin/servers/%d/catalog?done=%d", serverID, imported), http.StatusSeeOther)
}

// importUpstreamProduct 幂等导入：已按 (server_id, upstream_pid) 对接则更新价格/绑定/配置项，
// 否则新建分类+产品+价格+绑定+配置项。profitType/profitValue 仅对新建产品生效（不覆盖已有产品利润）。
// parentID：新建产品的分类归属（0=上游分组建一级；>0=建为该一级下的二级）。
func (m *AdminManage) importUpstreamProduct(ctx context.Context, sv *repo.Server, serverID int64, pid int, profitType int16, profitValue float64, parentID int64) bool {
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		return false
	}
	cfg := serverConfig(sv)
	list, err := prov.Catalog(ctx, cfg)
	if err != nil {
		return false
	}
	var up *server.UpstreamProduct
	for i := range list {
		if list[i].PID == pid {
			up = &list[i]
			break
		}
	}
	if up == nil {
		return false
	}
	psID, _ := m.Products.DefaultPricesetID(ctx)
	// 幂等：已对接则更新，未对接则新建
	existingID, _ := findProductByUpstream(ctx, m.Products.DB, serverID, int64(pid))
	var productID int64
	if existingID > 0 {
		productID = existingID
		// 幂等更新：同步上游描述（价格/绑定/配置项下方统一刷新）
		desc := cleanDesc(up.Description)
		if desc == "" {
			desc = fmt.Sprintf("导入自 %s（上游 PID %d）", sv.Name, pid)
		}
		if _, derr := m.Products.DB.ExecContext(ctx,
			`UPDATE products SET description=$2 WHERE id=$1`, productID, desc); derr != nil {
			log.Printf("[import] 更新描述失败 pid=%d: %v", pid, derr)
		}
	} else {
		typeID, terr := m.ensureType(ctx, up.GroupName, parentID)
		if terr != nil {
			return false
		}
		desc := cleanDesc(up.Description)
		if desc == "" {
			desc = fmt.Sprintf("导入自 %s（上游 PID %d）", sv.Name, pid)
		}
		productID, err = m.Products.Create(ctx, sqlNull(typeID), up.Name, desc, up.Stock)
		if err != nil {
			return false
		}
		// 新建产品设置导入利润
		if profitValue > 0 {
			m.Products.SetProfit(ctx, productID, profitType, profitValue)
		}
	}
	// 价格始终刷新（新建或更新均覆盖月/季/年）
	if _, err := m.Products.DB.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (product_id,priceset_id) DO UPDATE SET monthly=$3,quarterly=$4,yearly=$5`,
		productID, psID, money(up.Monthly), money(up.Quarterly), money(up.Yearly)); err != nil {
		return false
	}
	m.Products.SetBinding(ctx, productID, sqlNull(serverID), int64(pid))
	if fetcher, ok := prov.(server.ConfigOptionsFetcher); ok {
		if opts, err := fetcher.FetchProductConfigOptions(ctx, cfg, int64(pid)); err == nil {
			if len(opts) > 0 {
				m.Products.SaveConfigOptions(ctx, productID, opts)
			}
		}
	}
	return true
}

// descTagRe 去除上游描述里的 HTML 标签（如 <br>、<span>）。
var descTagRe = regexp.MustCompile(`<[^>]+>`)

// cleanDesc 把上游带 HTML 实体的描述清洗为纯文本：反转义 + 去标签 + 压缩空白。
func cleanDesc(s string) string {
	s = html.UnescapeString(s)
	s = descTagRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, f := range strings.Fields(s) {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f)
	}
	return strings.TrimSpace(b.String())
}

// ensureType 按分组名取分类 id，不存在则创建。
// parentID=0：分组名支持两级 "一级/二级"（上游返回嵌套时），单名建一级分类；
// parentID>0：管理员指定的一级分类，上游分组名（取末段）作为其下二级分类——
// 对齐 ZJMF 代理上游商品"必选本地分组"的模式（上游 /cart/all 多为单层分组）。
func (m *AdminManage) ensureType(ctx context.Context, groupName string, parentID int64) (int64, error) {
	first, second := groupName, ""
	if parts := strings.SplitN(groupName, "/", 2); len(parts) == 2 {
		first, second = parts[0], parts[1]
	}
	types, err := m.Products.ListTypes(ctx)
	if err != nil {
		return 0, err
	}
	if parentID > 0 {
		name := second
		if name == "" {
			name = first
		}
		for _, t := range types {
			if t.ParentID == parentID && t.Name == name {
				return t.ID, nil
			}
		}
		return m.Products.CreateType(ctx, name, "上游导入", 99, parentID, false)
	}
	// 定位/创建一级
	var fid int64
	for _, t := range types {
		if t.ParentID == 0 && t.Name == first {
			fid = t.ID
			break
		}
	}
	if fid == 0 {
		if fid, err = m.Products.CreateType(ctx, first, "上游导入", 99, 0, false); err != nil {
			return 0, err
		}
	}
	if second == "" {
		return fid, nil
	}
	// 定位/创建二级（仅限该一级下同名）
	for _, t := range types {
		if t.ParentID == fid && t.Name == second {
			return t.ID, nil
		}
	}
	return m.Products.CreateType(ctx, second, "上游导入", 99, fid, false)
}

// ---------- 服务管理 ----------

type adminServiceRow struct {
	ID       int64
	A, B, C  string // 用户 / 产品 / 状态
	D        string // 到期时间
	Upstream string // 上游 host id
	ProvErr  string // 开通失败原因
	Profit   string // 毛利（来自下单订单）
}

// ServicesList GET /admin/services — 全部服务列表。
func (m *AdminManage) ServicesList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	rows, err := m.Svc.DB.QueryContext(r.Context(),
		`SELECT sv.id, u.email, coalesce(sv.name,''), 
			CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
			to_char(coalesce(sv.expires_at, sv.created_at),'YYYY-MM-DD'),
			sv.upstream_host_id, coalesce(sv.provision_error,''),
			coalesce((SELECT profit FROM orders WHERE id=sv.order_id),'0')
		 FROM services sv JOIN users u ON u.id=sv.user_id WHERE sv.status < 3 ORDER BY sv.id DESC LIMIT 200`)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	defer rows.Close()
	var list []adminServiceRow
	for rows.Next() {
		var rw adminServiceRow
		rows.Scan(&rw.ID, &rw.A, &rw.B, &rw.C, &rw.D, &rw.Upstream, &rw.ProvErr, &rw.Profit)
		list = append(list, rw)
	}
	renderAdmin(w, "admin_services.html", AdminData{
		Rows: list, CSRF: csrfOf(adminSessions, w, r), Error: r.URL.Query().Get("err"),
	})
}

// ServicesStatusJSON GET /admin/services/status — 后台轮询用：返回各服务状态与失败原因，
// 用于自动发现“上游已开通/开通失败”并刷新列表（ponytail: 仅读 DB，不重复调上游）。
func (m *AdminManage) ServicesStatusJSON(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	rows, err := m.Svc.DB.QueryContext(r.Context(),
		`SELECT sv.id,
			CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
			coalesce(sv.provision_error,'')
		 FROM services sv WHERE sv.status < 3 ORDER BY sv.id DESC LIMIT 200`)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	defer rows.Close()
	type st struct {
		ID      int64  `json:"id"`
		C       string `json:"c"`
		ProvErr string `json:"provErr"`
	}
	var out []st
	for rows.Next() {
		var s st
		if err := rows.Scan(&s.ID, &s.C, &s.ProvErr); err != nil {
			break
		}
		out = append(out, s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ServiceAction POST /admin/services/{id}/action — 停机/解除/删除/重试开通。
func (m *AdminManage) ServiceAction(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PostFormValue("do")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var err error
	errMsg := ""
	switch action {
	case "suspend":
		err = m.Lifecycle.Suspend(ctx, id)
	case "unsuspend":
		err = m.Lifecycle.Unsuspend(ctx, id)
	case "terminate":
		err = m.Lifecycle.Terminate(ctx, id)
	case "retry":
		err = m.Payment.RetryProvision(ctx, id)
	default:
		http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
		return
	}
	if err != nil {
		errMsg = url.QueryEscape(err.Error())
	}
	m.audit(r, "service_"+action, "service", id, errMsg)
	http.Redirect(w, r, "/admin/services?err="+errMsg, http.StatusSeeOther)
}

// ---------- 用户管理操作 ----------

// UserEdit GET /admin/users/{id}/edit — 用户管理表单（余额/状态/重置密码）。
func (m *AdminManage) UserEdit(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var email, name string
	var status int16
	var balance float64
	err := m.Svc.DB.QueryRowContext(r.Context(),
		`SELECT email,name,status,balance::float8 FROM users WHERE id=$1`, id).
		Scan(&email, &name, &status, &balance)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderAdmin(w, "admin_user_form.html", AdminData{
		CSRF: csrfOf(adminSessions, w, r),
		ServersList: map[string]any{
			"ID": id, "A": email, "B": name,
			"C": fmt.Sprintf("%.2f", balance), "D": itoa(int64(status)),
		},
	})
}

// UserSave POST /admin/users/{id}/save — 状态/密码/余额调整。
func (m *AdminManage) UserSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	// 状态
	status := r.PostFormValue("status")
	if status == "0" || status == "1" {
		if err := m.Users.SetStatus(r.Context(), id, status == "1"); err != nil {
			http.Redirect(w, r, "/admin/users/"+itoa(id)+"/edit?err="+url.QueryEscape("保存状态失败"), http.StatusSeeOther)
			return
		}
	}
	// 重置密码（可选填写）
	if pw := r.PostFormValue("new_password"); pw != "" {
		if len(pw) < 8 {
			http.Redirect(w, r, "/admin/users/"+itoa(id)+"/edit?err=密码至少8位", http.StatusSeeOther)
			return
		}
		if err := m.Users.ResetPassword(r.Context(), id, pw); err != nil {
			http.Redirect(w, r, "/admin/users/"+itoa(id)+"/edit?err="+url.QueryEscape("重置密码失败"), http.StatusSeeOther)
			return
		}
	}
	// 余额调整（可选填写）
	if amtStr := strings.TrimSpace(r.PostFormValue("balance_adjust")); amtStr != "" {
		note := "管理员调整"
		if err := m.Balance.AdminAdjust(r.Context(), id, amtStr, note); err != nil {
			http.Redirect(w, r, "/admin/users/"+itoa(id)+"/edit?err="+err.Error(), http.StatusSeeOther)
			return
		}
	}
	m.audit(r, "user_update", "user", id, "status="+status)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// OrderRefund POST /admin/orders/{id}/refund — 管理员退款。
func (m *AdminManage) OrderRefund(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	amount := strings.TrimSpace(r.PostFormValue("amount"))
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	method := r.PostFormValue("method")
	// 外部支付（非余额）只能线下/手动标记已退款，禁止误退余额。
	var invMethod string
	_ = m.Payment.DB.QueryRowContext(r.Context(),
		`SELECT method FROM invoices WHERE order_id=$1 LIMIT 1`, id).Scan(&invMethod)
	if invMethod != "balance" {
		method = "gateway"
	} else if method != "gateway" {
		method = "balance"
	}
	var aid int64
	if s := middleware.FromSession(r.Context()); s != nil {
		aid = s.UserID
	}
	if err := m.Payment.Refund(r.Context(), aid, id, amount, reason, method); err != nil {
		http.Redirect(w, r, "/admin/orders?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	m.audit(r, "refund", "order", id, amount+" via "+method)
	http.Redirect(w, r, "/admin/refunds?ok=1", http.StatusSeeOther)
}

// syncProductUpstream 按需同步单个产品的上游价格与库存（编辑表单打开时调用）。
func (m *AdminManage) syncProductUpstream(ctx context.Context, p *repo.Product) {
	if !p.ServerID.Valid || p.UpstreamPID <= 0 {
		return
	}
	sv, err := m.Servers.Get(ctx, p.ServerID.Int64)
	if err != nil {
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		return
	}
	cfg := serverConfig(sv)
	// 拉取价格
	if fp, ok := prov.(server.PriceFetcher); ok {
		if mon, qtr, yr, ferr := fp.FetchProductPrice(ctx, cfg, p.UpstreamPID); ferr == nil {
			m.Products.UpdatePriceAndStock(ctx, p.ID, mon, qtr, yr, p.Stock)
		}
	}
	// 拉取库存（FetchProductMeta 返回 desc + stock）
	type metaFetcher interface {
		FetchProductMeta(ctx context.Context, cfg server.Config, upstreamPID int64) (string, int, error)
	}
	if mf, ok := prov.(metaFetcher); ok {
		if _, stock, merr := mf.FetchProductMeta(ctx, cfg, p.UpstreamPID); merr == nil {
			m.Products.DB.ExecContext(ctx, `UPDATE products SET stock=$2 WHERE id=$1`, p.ID, stock)
		}
	}
}

func loadGlobalProfitType(ctx context.Context, s *repo.Settings) int64 {
	if s == nil {
		return 0
	}
	v, _ := s.Get(ctx, "default_profit_type")
	n, _ := strconv.ParseInt(v, 10, 64)
	if n != 1 {
		n = 0
	}
	return n
}

func loadGlobalProfitValue(ctx context.Context, s *repo.Settings) float64 {
	if s == nil {
		return 0
	}
	v, _ := s.Get(ctx, "default_profit_value")
	f, _ := strconv.ParseFloat(v, 64)
	if f < 0 {
		f = 0
	}
	return f
}

// SaveGlobalProfit POST /admin/settings/profit — 保存全局默认利润。
func (m *AdminManage) SaveGlobalProfit(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) || !m.requireCSRF(w, r) {
		return
	}
	profitType, _ := strconv.ParseInt(r.PostFormValue("profit_type"), 10, 64)
	if profitType != 1 {
		profitType = 0
	}
	profitValue, _ := strconv.ParseFloat(r.PostFormValue("profit_value"), 64)
	if profitValue < 0 {
		profitValue = 0
	}
	m.Settings.Set(r.Context(), "default_profit_type", strconv.FormatInt(profitType, 10))
	m.Settings.Set(r.Context(), "default_profit_value", strconv.FormatFloat(profitValue, 'f', 2, 64))
	http.Redirect(w, r, "/admin/products?msg=全局利润已保存", http.StatusSeeOther)
}

var _ = context.Background
