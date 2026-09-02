package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type adminProductRow struct {
	ID               int64
	A, B, C          string // 名称 / 分类 / 月付价
	D                string // 显示状态
	ServerName       string
	UpstreamPID      int64
	RequiresIdentity bool
}

func (m *AdminManage) ProductsList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	psID, _ := m.Products.DefaultPricesetID(r.Context())
	list, err := m.Products.ListAdmin(r.Context(), psID)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	rows := make([]adminProductRow, 0, len(list))
	for _, p := range list {
		mn := "-"
		if p.Monthly != "" {
			mn = fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(p.Monthly), p.Options, p.ProfitType, p.ProfitValue))
		}
		h := "显示"
		if p.Hidden {
			h = "隐藏"
		}
		rows = append(rows, adminProductRow{
			ID: p.ID, A: p.Name, B: p.TypeName, C: mn, D: h,
			ServerName: p.ServerName, UpstreamPID: p.UpstreamPID, RequiresIdentity: p.RequiresIdentity,
		})
	}
	m.renderAdmin(w, "admin_products.html", AdminData{
		Rows: rows, CSRF: m.adminCSRF(w, r), Error: r.URL.Query().Get("err"),
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
	data := AdminData{Types: types, CSRF: m.adminCSRF(w, r)}
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
	m.renderAdmin(w, "admin_product_form.html", data)
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
				_ = m.Products.UpsertPrice(r.Context(), id, psID, money(pm), money(pq), money(py))
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
	requiresIdentity := r.PostFormValue("requires_identity") == "1"
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
		return m.Products.UpsertPrice(r.Context(), pid, psID, monthly, quarterly, yearly)
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
		if err == nil {
			err = m.Products.SetRequiresIdentity(r.Context(), pid, requiresIdentity)
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
		if err == nil {
			err = m.Products.SetRequiresIdentity(r.Context(), id, requiresIdentity)
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

func mustOpts(s string) []repo.ConfigOption {
	var opts []repo.ConfigOption
	json.Unmarshal([]byte(s), &opts) // 已在 save 前校验过
	return opts
}

// ---------- 上游目录浏览与导入（挂在 AdminManage，复用 Products/Users repo） ----------

// CatalogPage GET /admin/servers/{id}/catalog — 浏览上游商品目录。
