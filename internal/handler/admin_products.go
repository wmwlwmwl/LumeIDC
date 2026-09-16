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

func (m *AdminManage) ProductsList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	psID, _ := m.Products.DefaultPricesetID(r.Context())
	list, err := m.Products.ListAdmin(r.Context(), psID)
	if err != nil {
		log.Printf("[admin] 产品列表查询失败: %v", err)
		http.Error(w, "查询失败", 500)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		mn, first, cycle, label := "-", "-", "monthly", "月"
		monthly, quarterly, yearly := priceVal(p.Monthly), priceVal(p.Quarterly), priceVal(p.Yearly)
		_, cycle = service.AvailableCycles(monthly, quarterly, yearly)
		if cycle == "quarterly" {
			label = "季"
		} else if cycle == "yearly" {
			label = "年"
		}
		if cycle != "" {
			base := map[string]float64{"monthly": monthly, "quarterly": quarterly, "yearly": yearly}[cycle]
			mn = fmt.Sprintf("%.2f", service.DisplayPrice(base, p.Options, p.ProfitType, p.ProfitValue))
			first = fmt.Sprintf("%.2f", service.DisplayStartPrice(base, p.Options, p.ProfitType, p.ProfitValue, cycle))
		}
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.TypeName, "monthly": mn, "billing_cycle": cycle, "cycle_label": label, "first": first,
			"visible": !p.Hidden, "hidden": p.Hidden, "server": p.ServerName, "upstream_pid": p.UpstreamPID,
			"requires_identity": p.RequiresIdentity, "upstream_offline_reason": p.UpstreamOfflineReason,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

func (m *AdminManage) ProductForm(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	types, _ := m.Products.ListTypes(r.Context())
	data := AdminData{Types: types}
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
	// 各供应商产品表单区块（结构化声明：OptionsURL/SyncName/PullConfig/ConfigByValue）
	// —— 后台 SPA 渲染控件并执行联动，供应商无需再提供 HTML+脚本（见 docs/provider.md）。
	specByProvider := map[string][]server.ProductFormField{}
	for _, pi := range m.Providers.List() {
		if prov, err := m.Providers.Get(pi.Code); err == nil {
			if fields := server.ProductFormSpecFor(prov); len(fields) > 0 {
				specByProvider[pi.Code] = fields
			}
		}
	}
	if p, ok := data.Product.(*repo.Product); ok {
		data.UpstreamBound = p.ServerID.Valid && p.UpstreamPID > 0
	}
	typesJSON := make([]map[string]any, 0, len(types))
	for _, t := range types {
		typesJSON = append(typesJSON, map[string]any{"id": t.ID, "name": t.Name, "parent_id": t.ParentID, "hidden": t.Hidden})
	}
	servers := make([]map[string]any, 0, len(serversList))
	for _, sv := range serversList {
		servers = append(servers, map[string]any{"id": sv.ID, "name": sv.Name, "provider": sv.Provider})
	}
	// 各供应商的结构化表单声明（前端按所选服务器类型渲染）
	formSpec := map[string]any{}
	for code, fields := range specByProvider {
		formSpec[code] = fields
	}
	out := map[string]any{
		"ok": 1, "types": typesJSON, "servers": servers, "form_spec": formSpec,
		"prices": map[string]string{
			"monthly": data.Monthly, "quarterly": data.Quarterly, "yearly": data.Yearly,
		},
		"config_json":    data.ConfigJSON,
		"hints":          json.RawMessage(data.ProductHintsJSON),
		"upstream_bound": data.UpstreamBound,
	}
	if p, ok := data.Product.(*repo.Product); ok && p != nil {
		var typeID, serverID int64
		if p.TypeID.Valid {
			typeID = p.TypeID.Int64
		}
		if p.ServerID.Valid {
			serverID = p.ServerID.Int64
		}
		out["product"] = map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"type_id": typeID, "server_id": serverID, "upstream_pid": p.UpstreamPID,
			"stock": p.Stock, "hidden": p.Hidden, "requires_identity": p.RequiresIdentity,
			"profit_type": p.ProfitType, "profit_value": p.ProfitValue,
		}
	}
	writeJSON(w, out)
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
	// SPA 走 JSON（Accept: application/json），SSR 走表单；JSON 下 CSRF 由中间件按
	// X-CSRF-Token 校验，无需再读表单里的 _csrf。
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	fail := func(msg string) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/admin/products?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	success := func() {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
			return
		}
		http.Redirect(w, r, "/admin/products", http.StatusSeeOther)
	}
	name := strings.TrimSpace(fv("name"))
	if name == "" {
		fail("名称必填")
		return
	}
	desc := strings.TrimSpace(fv("description"))
	stock, _ := strconv.Atoi(fv("stock"))
	hidden := fv("hidden") == "1"
	requiresIdentity := fv("requires_identity") == "1"
	var typeID sql.NullInt64
	if v := fv("type_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		typeID = sql.NullInt64{Int64: id, Valid: true}
	}
	if !typeID.Valid {
		fail("请选择二级分类")
		return
	}
	types, terr := m.Products.ListTypes(r.Context())
	if terr != nil {
		fail("分类查询失败")
		return
	}
	if t, ok := repo.FindType(types, typeID.Int64); !ok || t.ParentID == 0 {
		fail("商品只能挂在二级分类下")
		return
	}
	psID, _ := m.Products.DefaultPricesetID(r.Context())
	monthly := normalizeAmount(fv("monthly"))
	quarterly := normalizeAmount(fv("quarterly"))
	yearly := normalizeAmount(fv("yearly"))
	configJSON := strings.TrimSpace(fv("configoption"))
	if configJSON != "" {
		var validate []repo.ConfigOption
		if err := json.Unmarshal([]byte(configJSON), &validate); err != nil {
			fail("配置项 JSON 格式错误: " + err.Error())
			return
		}
	}
	// 上游绑定
	var bindServer sql.NullInt64
	if v := fv("server_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		bindServer = sql.NullInt64{Int64: id, Valid: true}
	}
	upstreamPID, _ := strconv.ParseInt(fv("upstream_pid"), 10, 64)
	// 利润（对齐 ZJMF 上游利润）：0百分比 1固定金额
	profitType, _ := strconv.ParseInt(fv("profit_type"), 10, 64)
	if profitType != 1 {
		profitType = 0
	}
	profitValue, _ := strconv.ParseFloat(fv("profit_value"), 64)
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
	// id：编辑走路由 /admin/products/{id}/save，兼容表单隐藏域（SSR）。
	idStr := r.PathValue("id")
	if idStr == "" {
		idStr = fv("id")
	}
	if idStr == "" {
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
			fail(err.Error())
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
			fail(err.Error())
			return
		}
		m.audit(r, "product_update", "product", id, name)
	}
	success()
}

func (m *AdminManage) ProductDelete(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if jsonVals(r) == nil && !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := m.Products.Delete(r.Context(), id); err != nil {
		// 此前错误被丢弃，删除失败也会跳回列表（看起来像成功）
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/products?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	m.audit(r, "product_delete", "product", id, "")
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
		return
	}
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
