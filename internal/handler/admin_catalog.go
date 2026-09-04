package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// providerCatalogTTL 上游目录缓存有效期：目录数据低频变化，短 TTL 即可防止
// 多人反复打开页面/重复导入时对魔方财务的全量轰炸（浏览一次 1+N 次请求）。
const providerCatalogTTL = 60 * time.Second

// providerCatalogCache 管理端上游目录 TTL 缓存（仅目录页/导入复用）。
// 定时同步 syncPrices 直接调用 provider.Catalog 且不经过本缓存，价格必为实时。
// ponytail: 目录页/导入的价格可能滞后 ≤TTL；页面提供 ?fresh=1 强制刷新。
type providerCatalogCache struct {
	mu    sync.Mutex
	at    map[int64]time.Time
	lists map[int64][]server.UpstreamProduct
}

var catalogCache = &providerCatalogCache{
	at:    map[int64]time.Time{},
	lists: map[int64][]server.UpstreamProduct{},
}

func (c *providerCatalogCache) get(serverID int64) ([]server.UpstreamProduct, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.at[serverID]
	if !ok || time.Since(at) > providerCatalogTTL {
		return nil, false
	}
	return c.lists[serverID], true
}

func (c *providerCatalogCache) put(serverID int64, list []server.UpstreamProduct) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at[serverID] = time.Now()
	c.lists[serverID] = list
}

// providerCatalog 带 TTL 缓存的目录拉取；fresh=true 时强制重新请求上游。
func (m *AdminManage) providerCatalog(ctx context.Context, serverID int64, sv *repo.Server, prov server.Provider, fresh bool) ([]server.UpstreamProduct, error) {
	if !fresh {
		if list, ok := catalogCache.get(serverID); ok {
			return list, nil
		}
	}
	list, err := prov.Catalog(ctx, serverConfig(sv))
	if err != nil {
		return nil, err
	}
	catalogCache.put(serverID, list)
	return list, nil
}

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
	list, err := m.providerCatalog(ctx, id, sv, prov, r.URL.Query().Get("fresh") == "1")
	linked, lerr := m.Products.LinkedUpstreamPIDs(ctx, id)
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
	m.renderAdmin(w, "admin_catalog.html", AdminData{
		CSRF: m.adminCSRF(w, r),
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
		jsonFail(w, "server_id 无效")
		return
	}
	sv, err := m.Servers.Get(r.Context(), serverID)
	if err != nil {
		jsonFail(w, "读取服务器失败")
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var list []server.UpstreamProduct
	if cl, ok := prov.(server.CatalogLister); ok {
		list, err = cl.CatalogLight(ctx, serverConfig(sv))
	} else {
		list, err = m.providerCatalog(ctx, serverID, sv, prov, false)
	}
	if err != nil {
		jsonFail(w, "拉取目录失败: "+err.Error())
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
	jsonOK(w, "groups", out)
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
		jsonFail(w, "参数无效")
		return
	}
	sv, err := m.Servers.Get(r.Context(), serverID)
	if err != nil {
		jsonFail(w, "读取服务器失败")
		return
	}
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	fetcher, ok := prov.(server.ConfigOptionsFetcher)
	if !ok {
		jsonFail(w, "该供应商不支持配置项拉取")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	opts, err := fetcher.FetchProductConfigOptions(ctx, serverConfig(sv), int64(pid))
	if err != nil {
		jsonFail(w, err.Error())
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
	// 只拉一次目录（内含全部商品的名称/分组/价格/库存/描述），
	// 建 PID→商品 映射供各导入项复用，避免每导一个商品就全量拉一次目录——
	// 旧逻辑 N×M 次上游请求易触发限流/拉黑。
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/servers/%d/catalog?err=%s", serverID,
			url.QueryEscape("供应商错误")), http.StatusSeeOther)
		return
	}
	list, err := m.providerCatalog(ctx, serverID, sv, prov, false)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/servers/%d/catalog?err=%s", serverID,
			url.QueryEscape("拉取目录失败: "+err.Error())), http.StatusSeeOther)
		return
	}
	byPID := make(map[int]*server.UpstreamProduct, len(list))
	for i := range list {
		byPID[list[i].PID] = &list[i]
	}
	imported := 0
	for _, pidStr := range r.PostForm["import"] {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}
		up := byPID[pid]
		if up == nil {
			continue
		}
		if m.importUpstreamProduct(r.Context(), sv, serverID, up, int16(profitType), profitValue, parentID) {
			imported++
		}
	}
	http.Redirect(w, r,
		fmt.Sprintf("/admin/servers/%d/catalog?done=%d", serverID, imported), http.StatusSeeOther)
}

// importUpstreamProduct 幂等导入：已按 (server_id, upstream_pid) 对接则更新价格/绑定/配置项，
// 否则新建分类+产品+价格+绑定+配置项。profitType/profitValue 仅对新建产品生效（不覆盖已有产品利润）。
// parentID：新建产品的分类归属（0=上游分组建一级；>0=建为该一级下的二级）。
// up 由调用方一次性拉取目录后传入（本函数不再全量拉目录，避免导入 N 项触发 N×M 次上游请求）。

func (m *AdminManage) importUpstreamProduct(ctx context.Context, sv *repo.Server, serverID int64, up *server.UpstreamProduct, profitType int16, profitValue float64, parentID int64) bool {
	prov, err := m.Providers.Get(sv.Provider)
	if err != nil {
		return false
	}
	cfg := serverConfig(sv)
	pid := up.PID
	psID, _ := m.Products.DefaultPricesetID(ctx)
	// 幂等：已对接则更新，未对接则新建
	existingID, _ := m.Products.FindByUpstreamPID(ctx, serverID, int64(pid))
	var productID int64
	if existingID > 0 {
		productID = existingID
		// 幂等更新：同步上游描述（价格/绑定/配置项下方统一刷新）
		desc := cleanDesc(up.Description)
		if desc == "" {
			desc = fmt.Sprintf("导入自 %s（上游 PID %d）", sv.Name, pid)
		}
		if derr := m.Products.SetDescription(ctx, productID, desc); derr != nil {
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
	if err := m.Products.UpsertPrice(ctx, productID, psID, money(up.Monthly), money(up.Quarterly), money(up.Yearly)); err != nil {
		return false
	}
	m.Products.SetBinding(ctx, productID, sqlNull(serverID), int64(pid))
	// 配置项优先复用目录回填已拉取的（同一次 get_product_config，避免重复请求）；
	// 目录未带配置（如非 zjmf 供应商）时回退单独拉取。
	opts := up.ConfigOptions
	if len(opts) == 0 {
		if fetcher, ok := prov.(server.ConfigOptionsFetcher); ok {
			if fetched, err := fetcher.FetchProductConfigOptions(ctx, cfg, int64(pid)); err == nil {
				opts = fetched
			}
		}
	}
	if len(opts) > 0 {
		m.Products.SaveConfigOptions(ctx, productID, opts)
	}
	return true
}

// cleanDesc 保留上游描述的换行和安全 HTML，危险标签与属性由统一过滤器移除。

func cleanDesc(s string) string {
	return strings.TrimSpace(string(safeDescriptionHTML(s)))
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
			_ = m.Products.SetStock(ctx, p.ID, stock)
		}
	}
}
