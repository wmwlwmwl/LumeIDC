package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/plugin"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
	"lumeidc/internal/storage"
)

type Pages struct {
	DB            *sql.DB
	PrivateFiles  *storage.PrivateFiles
	Products      *repo.Products
	Svc           *service.ServicesRepo
	Orders        *service.Orders
	UsersRepo     *repo.Users
	ServersRepo   *repo.Servers
	Console       *service.Console
	Balance       *repo.Balance
	Notifier      *service.Notifier
	Announcements *repo.Announcements
	Settings      *repo.Settings
	Invoices      *repo.Invoices
	Promotion     *service.PromotionService
	CancelReqs    *repo.CancelRequests // 用户停用申请
	Payment       *service.Payment     // 降级 0 元单余额核销用
	*Deps
	// 仅服务列表补全共享额度，不限制详情页、电源等操作。
	serviceListOnce  sync.Once
	serviceListSlots chan struct{}
}

func (h *Pages) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /products", h.products)
	mux.HandleFunc("GET /cart", h.cart)
	// 营销活动页
	mux.HandleFunc("GET /promotions", h.promotionList)
	mux.HandleFunc("GET /promotion/{id}", h.promotionDetail)
	mux.HandleFunc("POST /promotion/{id}/claim", h.promotionClaim)
	mux.HandleFunc("GET /services", h.myServices)
	mux.HandleFunc("GET /buy/{productID}", h.buyForm)
	mux.HandleFunc("GET /user", h.userHome)
	mux.HandleFunc("GET /user/recharge", h.rechargeForm)
	mux.HandleFunc("POST /user/recharge", h.rechargeSubmit)
	mux.HandleFunc("GET /user/promotion-coupons", h.userPromotionCoupons)
	mux.HandleFunc("GET /user/invoices", h.userInvoices)
	mux.HandleFunc("GET /services/{serviceID}", h.serviceDetail)
	mux.HandleFunc("POST /services/{serviceID}/refresh", h.serviceRefresh)
	mux.HandleFunc("GET /services/{serviceID}/invoices", h.serviceInvoices)
	mux.HandleFunc("POST /services/{serviceID}/renew", h.serviceRenew)
	mux.HandleFunc("POST /services/{serviceID}/name", h.serviceRename)
	mux.HandleFunc("GET /services/{serviceID}/upgrade", h.serviceUpgradeForm)
	mux.HandleFunc("POST /services/{serviceID}/upgrade", h.serviceUpgradeOrder)
	mux.HandleFunc("POST /services/{serviceID}/cancel-request", h.serviceCancelRequestSubmit)
	mux.HandleFunc("GET /services/{serviceID}/cancel-request", h.serviceCancelRequestInfo)
	mux.HandleFunc("POST /services/{serviceID}/cancel-request/withdraw", h.serviceCancelRequestWithdraw)
	mux.HandleFunc("POST /services/{serviceID}/console", h.consoleAction)
	// VNC 控制台页面由 SPA 承载（/services/{id}/console），后端只保留隧道与会话密码。
	mux.HandleFunc("GET /services/{serviceID}/vnc-ws", h.vncWebSocket)
	mux.HandleFunc("GET /services/{serviceID}/vnc-pass", h.serviceVncPass)
	mux.HandleFunc("GET /services/{serviceID}/chart", h.serviceChart)
	mux.HandleFunc("GET /services/{serviceID}/usage", h.serviceUsage)
	mux.HandleFunc("GET /services/{serviceID}/power", h.servicePower)
	mux.HandleFunc("GET /services/{serviceID}/traffic", h.serviceTraffic)
	mux.HandleFunc("GET /services/{serviceID}/snapshot", h.serviceSnapshot)
	mux.HandleFunc("POST /services/{serviceID}/snapshot/{fn}", h.serviceSnapshotAction)
	mux.HandleFunc("GET /services/{serviceID}/blocks", h.serviceBlocks)
	mux.HandleFunc("POST /services/{serviceID}/block/{fn}", h.serviceBlockAction)
	mux.HandleFunc("GET /services/{serviceID}/block-rules", h.serviceBlockRules)
	mux.HandleFunc("GET /services/{serviceID}/rescue-state", h.serviceRescueState)
	mux.HandleFunc("GET /services/{serviceID}/reinstall-options", h.reinstallOptions)
	mux.HandleFunc("GET /user/password", h.passwordForm)
	mux.HandleFunc("POST /user/password", h.passwordSubmit)
	mux.HandleFunc("GET /notifications", h.notifications)
	mux.HandleFunc("GET /notifications/unread-count", h.notificationUnreadCount)
	mux.HandleFunc("POST /notifications/{notificationID}/read", h.notificationMarkRead)
	mux.HandleFunc("POST /notifications/read-all", h.notificationMarkAllRead)
	mux.HandleFunc("POST /notifications/{notificationID}/delete", h.notificationDelete)
	mux.HandleFunc("POST /notifications/delete-all", h.notificationDeleteAll)
	// 工单 API 由 tickets 插件提供（/plugin/tickets/...）；页面路由见前台 SPA。
	// 公告中心 API 由 announcement 插件提供（/plugin/announcement/list|detail/{id}）。
}

// NotFound 全局 404（未匹配路由的统一兜底）。
func (h *Pages) NotFound(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// notifications GET /notifications — 站内信列表（查看列表不自动标记已读）。
func (h *Pages) notifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Notifier == nil {
		writeJSON(w, map[string]any{"ok": 1, "list": []any{}, "total": 0, "unread": 0, "page": 1, "limit": 10})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}
	list, total, unread, err := h.Notifier.ListPage(r.Context(), userID,
		strings.TrimSpace(r.URL.Query().Get("category")), strings.TrimSpace(r.URL.Query().Get("keyword")), page, limit)
	if err != nil {
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list, "total": total, "unread": unread, "page": page, "limit": limit,
		"categories": []map[string]string{{"key": "", "label": "全部"}, {"key": "order", "label": "订单"}, {"key": "payment", "label": "支付"}, {"key": "service", "label": "服务"}, {"key": "identity", "label": "实名"}, {"key": "system", "label": "系统"}}})
}

// listAnnouncements 取前台展示的公告（显示中、置顶优先）。
// 公告数据表由 announcement 插件拥有；插件禁用时返回空（首页新闻区/用户中心公告自动隐藏）。
func (h *Pages) listAnnouncements(ctx context.Context) []map[string]any {
	if h.Announcements == nil || !plugin.Enabled("announcement") {
		return nil
	}
	list, err := h.Announcements.List(ctx, true)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, an := range list {
		out = append(out, map[string]any{
			"ID": an.ID, "Title": an.Title, "Category": an.Category, "Summary": an.Summary,
			"Content": an.Content, "Cover": an.Cover, "Pinned": an.Pinned, "Reads": an.Reads,
			"CreatedAt": an.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

type productView struct {
	ID           int64
	Name         string
	Desc         string
	Monthly      string
	BillingCycle string
	CycleLabel   string
	Stock        int
}

// productListJSON 前台产品 → JSON 视图（SPA 用，键小写对齐 API 约定）。
func productListJSON(views []productView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		out = append(out, map[string]any{
			"id": v.ID, "name": v.Name, "desc": v.Desc, "monthly": v.Monthly,
			"billing_cycle": v.BillingCycle, "cycle_label": v.CycleLabel, "stock": v.Stock,
		})
	}
	return out
}

// typeNavJSON 分类导航 → JSON 视图。
func typeNavJSON(nav []typeNav) []map[string]any {
	out := make([]map[string]any, 0, len(nav))
	for _, first := range nav {
		children := make([]map[string]any, 0, len(first.Children))
		for _, c := range first.Children {
			children = append(children, map[string]any{
				"id": c.ID, "name": c.Name, "description": c.Description, "hidden": c.Hidden,
			})
		}
		out = append(out, map[string]any{"id": first.ID, "name": first.Name, "children": children})
	}
	return out
}

// announcementJSON 公告（listAnnouncements 的 PascalCase map）→ 小写键 JSON 视图。
func announcementJSON(list []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, an := range list {
		out = append(out, map[string]any{
			"id": an["ID"], "title": an["Title"], "category": an["Category"], "summary": an["Summary"],
			"content": an["Content"], "cover": an["Cover"], "pinned": an["Pinned"], "reads": an["Reads"],
			"created_at": an["CreatedAt"],
		})
	}
	return out
}

var descriptionTagRe = regexp.MustCompile(`(?is)<!--.*?-->|</?\s*([a-z][a-z0-9]*)[^>]*>`)

// 危险标签要连内文一起丢：上游描述里内嵌 <style>（还有少数带 <script>），
// 只去标签会把 CSS/JS 源码当正文渲染出来（列表卡片就会显示一堆 .config-row {…}）。
var descriptionDropRe = regexp.MustCompile(`(?is)<\s*script\b[^>]*>.*?<\s*/\s*script\s*>|<\s*style\b[^>]*>.*?<\s*/\s*style\s*>`)

// 连续换行折叠成一个、首尾不要换行（块级标签一头一尾会各补一个）。
var descriptionBrRunRe = regexp.MustCompile(`(?i)(?:\s*<br\s*/?>\s*){2,}`)

// 换行两侧的空格（横向容器里的列用空格拼接，可能落在换行边上）、连续空格。
var descriptionBrSpaceRe = regexp.MustCompile(`(?i)\s*<br\s*/?>\s*`)
var descriptionSpaceRunRe = regexp.MustCompile(` {2,}`)

// 内联 style、以及 <style> 里的「类名 { 声明 }」，用于判断容器是不是横向排布。
var descriptionStyleAttrRe = regexp.MustCompile(`(?i)\bstyle\s*=\s*["']([^"']*)["']`)
var descriptionStyleRuleRe = regexp.MustCompile(`(?is)\.([a-z0-9_-]+)\s*\{([^}]*)\}`)

// href 属性、以及放行外链用的 scheme 白名单（只放行 http/https，挡掉 javascript:/data: 等）。
var descriptionHrefRe = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']*)["']`)
var descriptionSafeURLRe = regexp.MustCompile(`(?i)^https?://[^\s<>"']+$`)

// rowContainerClasses 从描述内嵌 <style> 里抽「横向容器」类名：display 为 flex/grid 且没写成 column。
// 上游商品描述常是「外层 flex-column 堆行、.config-row 内 flex 行放图标+标签+值」，
// 转文本时要按这个层级决定哪里换行、哪里用空格。
func rowContainerClasses(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range descriptionStyleRuleRe.FindAllStringSubmatch(s, -1) {
		if isRowDisplay(strings.ToLower(m[2])) {
			out[m[1]] = true
		}
	}
	return out
}

// isRowDisplay 声明体是否横向排布（flex/grid 且未声明 column）。
func isRowDisplay(decl string) bool {
	d := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(decl))
	if !strings.Contains(d, "display:flex") && !strings.Contains(d, "display:inline-flex") && !strings.Contains(d, "display:grid") {
		return false
	}
	return !strings.Contains(d, "flex-direction:column") && !strings.Contains(d, "grid-auto-flow:row")
}

// rowContainer 该容器是否横向排布：内联 style 优先，其次看类名在内嵌样式里的声明。
func rowContainer(full string, rowClasses map[string]bool) bool {
	if sm := descriptionStyleAttrRe.FindStringSubmatch(full); len(sm) == 2 {
		if strings.Contains(strings.ToLower(sm[1]), "display:") {
			return isRowDisplay(sm[1])
		}
	}
	if cm := descriptionClassRe.FindStringSubmatch(full); len(cm) == 2 {
		for _, name := range strings.Fields(strings.ToLower(cm[1])) {
			if rowClasses[name] {
				return true
			}
		}
	}
	return false
}

// descriptionBlockTags 块级标签：白名单外会被去掉壳，去掉时补一个换行。
// 上游不少商品描述是「一行一个 div」（如 <div class="config-row">CPU…</div>），
// 只去壳不留断行的话，规格会全部粘成一坨（"CPU16核 intel E5内存32GB…"）。
var descriptionBlockTags = map[string]bool{
	"div": true, "p": true, "section": true, "article": true, "header": true, "footer": true,
	"main": true, "aside": true, "nav": true, "figure": true, "blockquote": true, "pre": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "td": true, "th": true,
	"dl": true, "dt": true, "dd": true, "hr": true, "center": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}
var descriptionClassRe = regexp.MustCompile(`(?i)\bclass\s*=\s*["']([a-z0-9_ -]+)["']`)
var descriptionClassNameRe = regexp.MustCompile(`^[a-z0-9_-]+$`)
var multiSpaceRe = regexp.MustCompile(`[\s\x{3000}]+`)
var inlineBreakRe = regexp.MustCompile(`([^\s<>])\s*(<(?:b|strong)>)`)

// normSpace 将连续空白（含全角空格）压缩为单个半角空格。
func normSpace(s string) string {
	return strings.TrimSpace(multiSpaceRe.ReplaceAllString(s, " "))
}

// safeDescriptionHref 取描述里 <a> 的安全外链地址：只放行 http/https，
// 挡掉 javascript:/data: 等可执行 scheme（上游描述是外部内容，不能当成可信标签用）。
func safeDescriptionHref(full string) (string, bool) {
	m := descriptionHrefRe.FindStringSubmatch(full)
	if len(m) != 2 {
		return "", false
	}
	href := strings.TrimSpace(html.UnescapeString(m[1]))
	if !descriptionSafeURLRe.MatchString(href) {
		return "", false
	}
	return href, true
}

// safeDescriptionHTML 仅保留无属性的排版标签，避免描述成为脚本入口。
// 块级标签（白名单外的 div/section/table…）去掉壳时补分隔符：默认换行；
// 若该容器按内联 style / 描述内嵌样式是横向排布（flex 行、grid），其子元素改用空格拼接，
// 于是上游「图标 标签 值」一行多列的规格，转成文本后仍是「CPU 16核 intel E5」一行。
func safeDescriptionHTML(s string) template.HTML {
	s = html.UnescapeString(s)
	rowClasses := rowContainerClasses(s)          // 必须在丢弃 <style> 之前抽取
	s = descriptionDropRe.ReplaceAllString(s, "") // <style>/<script> 连内容一起丢弃
	allowed := map[string]bool{"p": true, "br": true, "strong": true, "b": true, "em": true, "i": true, "ul": true, "ol": true, "li": true, "span": true}
	var b strings.Builder
	last := 0
	// 容器上下文栈：栈顶 true = 当前容器横向排布（它的兄弟元素之间用空格，不换行）。
	inlineCtx := []bool{false}
	// 已放行的 <a> 层数：href 不合法的链接整对丢掉，别留下孤立的 </a>。
	linkDepth := 0
	sep := func() string {
		if inlineCtx[len(inlineCtx)-1] {
			return " "
		}
		return "<br>"
	}
	for _, m := range descriptionTagRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(template.HTMLEscapeString(normSpace(s[last:m[0]])))
		full := s[m[0]:m[1]]
		tag := ""
		if m[2] >= 0 && m[3] >= 0 {
			tag = strings.ToLower(s[m[2]:m[3]])
		}
		closing := strings.HasPrefix(strings.TrimSpace(full)[1:], "/")
		switch {
		case tag == "a":
			// 外链只放行 http/https，并统一加固（新窗口 + noopener/nofollow）。
			if closing {
				if linkDepth > 0 {
					b.WriteString("</a>")
					linkDepth--
				}
			} else if href, ok := safeDescriptionHref(full); ok {
				b.WriteString(`<a href="` + template.HTMLEscapeString(href) +
					`" target="_blank" rel="noopener noreferrer nofollow">`)
				linkDepth++
			}
		case allowed[tag]:
			switch {
			case closing:
				b.WriteString("</" + tag + ">")
			case tag == "br":
				b.WriteString("<br>")
			default:
				class := ""
				if cm := descriptionClassRe.FindStringSubmatch(full); len(cm) == 2 {
					var names []string
					for _, name := range strings.Fields(strings.ToLower(cm[1])) {
						if descriptionClassNameRe.MatchString(name) {
							names = append(names, name)
						}
					}
					if len(names) > 0 {
						class = ` class="` + template.HTMLEscapeString(strings.Join(names, " ")) + `"`
					}
				}
				b.WriteString("<" + tag + class + ">")
			}
		case descriptionBlockTags[tag]:
			// 壳去掉但结构不能丢：容器边界补分隔符（换行，横向容器内是空格）。
			if closing {
				if len(inlineCtx) > 1 {
					inlineCtx = inlineCtx[:len(inlineCtx)-1]
				}
				b.WriteString(sep())
			} else {
				b.WriteString(sep())
				inlineCtx = append(inlineCtx, rowContainer(full, rowClasses))
			}
		}
		last = m[1]
	}
	b.WriteString(template.HTMLEscapeString(normSpace(s[last:])))
	out := b.String()
	// 相邻 <b>/<strong> 无换行时自动插入 <br>（上游 "标签</b>值<b>标签" 格式）
	out = inlineBreakRe.ReplaceAllString(out, "$1<br>$2")
	out = strings.TrimSpace(out)
	out = descriptionBrSpaceRe.ReplaceAllString(out, "<br>")
	out = descriptionBrRunRe.ReplaceAllString(out, "<br>")
	out = descriptionSpaceRunRe.ReplaceAllString(out, " ")
	out = strings.TrimSuffix(strings.TrimPrefix(out, "<br>"), "<br>")
	return template.HTML(out)
}

// typeNav 前台分类导航（两级）。
type typeNav struct {
	ID       int64
	Name     string
	Children []repo.ProductType
}

// buildTypeNav 扁平分类 → 可见两级导航：一级隐藏则其下二级一并隐藏。
// ponytail: 内存过滤，分类量为个位/十位级；若过百再改 SQL 递归。
func buildTypeNav(types []repo.ProductType) []typeNav {
	byID := make(map[int64]*repo.ProductType, len(types))
	for i := range types {
		byID[types[i].ID] = &types[i]
	}
	var nav []typeNav
	idx := map[int64]int{}
	for _, t := range types {
		if t.ParentID == 0 && !t.Hidden {
			idx[t.ID] = len(nav)
			nav = append(nav, typeNav{ID: t.ID, Name: t.Name})
		}
	}
	for _, t := range types {
		if t.ParentID == 0 || t.Hidden {
			continue
		}
		if parent, ok := byID[t.ParentID]; !ok || parent.Hidden {
			continue
		}
		if i, ok := idx[t.ParentID]; ok {
			nav[i].Children = append(nav[i].Children, t)
		}
	}
	return nav
}

// typeVisible 分类可见：自身未隐藏且（若为二级）父分类未隐藏。
func typeVisible(t repo.ProductType, types []repo.ProductType) bool {
	if t.Hidden {
		return false
	}
	if t.ParentID != 0 {
		p, ok := repo.FindType(types, t.ParentID)
		return ok && !p.Hidden
	}
	return true
}

func (h *Pages) home(w http.ResponseWriter, r *http.Request) {
	types, err := h.Products.ListTypes(r.Context())
	if err != nil {
		http.Error(w, "读取产品失败", 500)
		return
	}
	nav := buildTypeNav(types)
	// ponytail: 批量取数——分类产品、价格、配置选项、利润回退各 1 条 SQL，
	// 原 N+1（每产品 6 条 × 8 + 每分类 1 条 ≈ 50+ 条）降为固定 6 条；
	// 批量失败时价格/选项/利润按缺省值降级，产品列表回退逐分类查询。
	// 顺序语义与逐分类查询一致：按分类导航顺序取产品，凑满 8 个为止。
	var leafIDs []int64
	for _, first := range nav {
		for _, leaf := range first.Children {
			if typeVisible(leaf, types) {
				leafIDs = append(leafIDs, leaf.ID)
			}
		}
	}
	byType := make(map[int64][]repo.Product)
	if len(leafIDs) > 0 {
		all, lerr := h.Products.ListVisibleByTypes(r.Context(), leafIDs)
		if lerr != nil {
			// 批量查询失败：回退逐分类查询，保留"单分类失败只跳过该分类"的降级粒度
			log.Printf("[home] 批量查询产品失败，回退逐分类查询: %v", lerr)
			for _, leafID := range leafIDs {
				if list, err := h.Products.ListVisibleByTypes(r.Context(), []int64{leafID}); err == nil {
					byType[leafID] = list
				}
			}
		} else {
			for _, p := range all {
				if p.TypeID.Valid {
					byType[p.TypeID.Int64] = append(byType[p.TypeID.Int64], p)
				}
			}
		}
	}
	var picked []repo.Product
outer:
	for _, first := range nav {
		for _, leaf := range first.Children {
			for _, p := range byType[leaf.ID] {
				if len(picked) >= 8 {
					break outer
				}
				picked = append(picked, p)
			}
		}
	}
	ids := make([]int64, len(picked))
	for i, p := range picked {
		ids[i] = p.ID
	}
	psID, _ := h.Products.DefaultPricesetID(r.Context())
	prices := h.Products.PricesByProduct(r.Context(), psID, ids)
	optsBy, err := h.Products.ConfigOptionsByProducts(r.Context(), ids)
	if err != nil {
		log.Printf("[home] 批量读取产品配置失败: %v", err)
		optsBy = map[int64][]repo.ConfigOption{}
	}
	fallbacks := h.Products.ServerProfitFallbacks(r.Context(), ids)
	views := make([]productView, 0, len(picked))
	for _, p := range picked {
		m, cycle, label := "-", "monthly", "月"
		if pr, ok := prices[p.ID]; ok {
			eType, eVal := p.ProfitType, p.ProfitValue
			if p.ProfitValue <= 0 {
				fb := fallbacks[p.ID]
				eType, eVal = fb.Type, fb.Value
			}
			monthly, quarterly, yearly := priceVal(pr.Monthly), priceVal(pr.Quarterly), priceVal(pr.Yearly)
			if _, selected := service.AvailableCycles(monthly, quarterly, yearly); selected != "" {
				cycle = selected
			}
			if cycle == "quarterly" {
				label = "季"
			} else if cycle == "yearly" {
				label = "年"
			}
			base := map[string]float64{"monthly": monthly, "quarterly": quarterly, "yearly": yearly}[cycle]
			m = fmt.Sprintf("%.2f", service.DisplayStartPrice(base, optsBy[p.ID], eType, eVal, cycle))
		}
		views = append(views, productView{ID: p.ID, Name: p.Name, Desc: p.Description, Monthly: m, BillingCycle: cycle, CycleLabel: label, Stock: p.Stock})
	}
	writeJSON(w, map[string]any{
		"catalog":       typeNavJSON(nav),
		"products":      productListJSON(views),
		"announcements": announcementJSON(h.listAnnouncements(r.Context())),
		"promotions":    h.homePromotions(r.Context()),
	})
}

// homePromotions 首页活动入口：进行中优先，其次即将开始，最多 4 个；已结束不展示。
func (h *Pages) homePromotions(ctx context.Context) []map[string]any {
	if h.Promotion == nil || h.Promotion.Promo == nil {
		return []map[string]any{}
	}
	all, err := h.Promotion.Promo.List(ctx)
	if err != nil || len(all) == 0 {
		return []map[string]any{}
	}
	ongoing := make([]repo.Promotion, 0, 4)
	upcoming := make([]repo.Promotion, 0, 4)
	for _, p := range all {
		if !p.Enabled || p.Status() == "ended" {
			continue
		}
		switch p.Status() {
		case "ongoing":
			ongoing = append(ongoing, p)
		case "upcoming":
			upcoming = append(upcoming, p)
		}
	}
	// List 已按 id DESC（新建在前）；进行中排在即将开始之前。
	picked := append(ongoing, upcoming...)
	if len(picked) > 4 {
		picked = picked[:4]
	}
	out := make([]map[string]any, 0, len(picked))
	for _, p := range picked {
		out = append(out, map[string]any{
			"id":          p.ID,
			"name":        p.Name,
			"description": p.Description,
			"type":        p.Type,
			"banner":      p.Banner,
			"ends_at":     p.EndsAt.Format(time.RFC3339),
			"status":      p.Status(),
		})
	}
	return out
}

func (h *Pages) products(w http.ResponseWriter, r *http.Request) {
	types, err := h.Products.ListTypes(r.Context())
	if err != nil {
		http.Error(w, "读取产品失败", 500)
		return
	}
	// 对齐魔方财务：产品入口默认选中第一个有二级分类的一级/二级分类。
	for _, first := range buildTypeNav(types) {
		if len(first.Children) == 0 {
			continue
		}
		http.Redirect(w, r, fmt.Sprintf("/cart?fid=%d&gid=%d", first.ID, first.Children[0].ID), http.StatusSeeOther)
		return
	}
	// 没有可选二级分类时：SPA 请求返回分类数据；浏览器导航交给 SPA 的 /cart。
	if wantsJSON(r) {
		h.productListPage(w, r, "/products")
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

// cart 对齐魔方财务：一级 fid 与二级 gid 分开传递。
func (h *Pages) cart(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("gid") == "" {
		types, err := h.Products.ListTypes(r.Context())
		if err != nil {
			http.Error(w, "读取产品失败", 500)
			return
		}
		fid, err := strconv.ParseInt(r.URL.Query().Get("fid"), 10, 64)
		if err == nil {
			for _, first := range buildTypeNav(types) {
				if first.ID == fid && len(first.Children) > 0 {
					http.Redirect(w, r, fmt.Sprintf("/cart?fid=%d&gid=%d", fid, first.Children[0].ID), http.StatusSeeOther)
					return
				}
			}
		}
	}
	h.productListPage(w, r, "/cart")
}

func (h *Pages) productListPage(w http.ResponseWriter, r *http.Request, _ string) {
	types, err := h.Products.ListTypes(r.Context())
	if err != nil {
		http.Error(w, "读取产品失败", 500)
		return
	}
	nav := buildTypeNav(types)
	fid, gid := "", ""
	var views []productView
	// 对齐 ZJMF：fid 只定位一级导航，gid 必须是 fid 下的二级分类才加载商品。
	firstID, ferr := strconv.ParseInt(r.URL.Query().Get("fid"), 10, 64)
	secondID, gerr := strconv.ParseInt(r.URL.Query().Get("gid"), 10, 64)
	if ferr == nil {
		if first, ok := repo.FindType(types, firstID); ok && first.ParentID == 0 && typeVisible(first, types) {
			fid = strconv.FormatInt(firstID, 10)
			if gerr == nil {
				if second, ok := repo.FindType(types, secondID); ok && second.ParentID == firstID && typeVisible(second, types) {
					gid = strconv.FormatInt(secondID, 10)
					list, lerr := h.Products.ListVisibleByTypes(r.Context(), []int64{second.ID})
					if lerr != nil {
						http.Error(w, "读取产品失败", 500)
						return
					}
					ids := make([]int64, len(list))
					for i, p := range list {
						ids[i] = p.ID
					}
					psID, _ := h.Products.DefaultPricesetID(r.Context())
					prices := h.Products.PricesByProduct(r.Context(), psID, ids)
					optsBy, optsErr := h.Products.ConfigOptionsByProducts(r.Context(), ids)
					if optsErr != nil {
						log.Printf("[home] 批量读取产品配置失败: %v", optsErr)
						optsBy = map[int64][]repo.ConfigOption{}
					}
					fallbacks := h.Products.ServerProfitFallbacks(r.Context(), ids)
					for _, p := range list {
						m, cycle, label := "-", "monthly", "月"
						if pr, ok := prices[p.ID]; ok {
							eType, eVal := p.ProfitType, p.ProfitValue
							if p.ProfitValue <= 0 {
								fb := fallbacks[p.ID]
								eType, eVal = fb.Type, fb.Value
							}
							monthly, quarterly, yearly := priceVal(pr.Monthly), priceVal(pr.Quarterly), priceVal(pr.Yearly)
							if _, selected := service.AvailableCycles(monthly, quarterly, yearly); selected != "" {
								cycle = selected
							}
							if cycle == "quarterly" {
								label = "季"
							} else if cycle == "yearly" {
								label = "年"
							}
							base := map[string]float64{"monthly": monthly, "quarterly": quarterly, "yearly": yearly}[cycle]
							m = fmt.Sprintf("%.2f", service.DisplayStartPrice(base, optsBy[p.ID], eType, eVal, cycle))
						}
						views = append(views, productView{ID: p.ID, Name: p.Name, Desc: p.Description, Monthly: m, BillingCycle: cycle, CycleLabel: label, Stock: p.Stock})
					}
				}
			}
		}
	}
	writeJSON(w, map[string]any{
		"catalog": typeNavJSON(nav), "products": productListJSON(views),
		"fid": fid, "gid": gid,
		"announcements": announcementJSON(h.listAnnouncements(r.Context())),
	})
}

func (h *Pages) buyForm(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "productID")
	p, err := h.Products.Get(r.Context(), id)
	if err != nil || p.Hidden || p.UpstreamOfflineReason != "" {
		http.NotFound(w, r)
		return
	}
	if sellable, serr := h.Products.IsSellable(r.Context(), id); serr != nil || !sellable {
		http.NotFound(w, r)
		return
	}
	psID, _ := h.Products.DefaultPricesetID(r.Context())
	pr, err := h.Products.Price(r.Context(), p.ID, psID)
	if err != nil {
		http.Error(w, "该商品未配置价格", 400)
		return
	}
	opts, _ := h.Products.GetConfigOptions(r.Context(), p.ID)
	monthly, quarterly, yearly := priceVal(pr.Monthly), priceVal(pr.Quarterly), priceVal(pr.Yearly)
	cycles, defaultCycle := service.AvailableCycles(monthly, quarterly, yearly)
	// 周期是否开售：价格>0 才展示；三周期基础价均为 0 时保留月付的配置计价/免费商品入口。
	showMonthly := len(cycles) == 0 || monthly > 0
	showQ := quarterly > 0
	showY := yearly > 0
	if defaultCycle == "" {
		defaultCycle = "monthly"
	}
	// 客户端实时计价数据：周期基础价 + 各配置项加价表（提交后服务端仍会重算）
	baseMap := map[string]float64{}
	if v, err := strconv.ParseFloat(pr.Monthly, 64); err == nil {
		baseMap["monthly"] = v
	}
	if v, err := strconv.ParseFloat(pr.Quarterly, 64); err == nil {
		baseMap["quarterly"] = v
	}
	if v, err := strconv.ParseFloat(pr.Yearly, 64); err == nil {
		baseMap["yearly"] = v
	}
	// 周期下拉展示价：配置计价型（基础价 0）用加成后起步价，普通产品直接加成基础价。
	eType, eVal := productProfitType(r.Context(), h.Products, p.ProfitType, p.ProfitValue, p.ID), productProfitValue(r.Context(), h.Products, p.ProfitType, p.ProfitValue, p.ID)
	dispMonthly := fmt.Sprintf("%.2f", service.DisplayStartPrice(priceVal(pr.Monthly), opts, eType, eVal, "monthly"))
	dispQuarterly := fmt.Sprintf("%.2f", service.DisplayStartPrice(priceVal(pr.Quarterly), opts, eType, eVal, "quarterly"))
	dispYearly := fmt.Sprintf("%.2f", service.DisplayStartPrice(priceVal(pr.Yearly), opts, eType, eVal, "yearly"))
	// SPA 复刻旧 buy.html 的客户端实时计价：下发原价表 + 配置项 + 利润，前端重算展示价。
	writeJSON(w, map[string]any{
		"product": map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"requires_identity": p.RequiresIdentity, "stock": p.Stock, "hidden": p.Hidden,
			"upstream_offline_reason": p.UpstreamOfflineReason,
		},
		"base":          baseMap,
		"cycle":         map[string]any{"monthly": dispMonthly, "quarterly": dispQuarterly, "yearly": dispYearly},
		"cycles":        cycles,
		"default_cycle": defaultCycle,
		"show_monthly":  showMonthly,
		"show_q":        showQ,
		"show_y":        showY,
		"options":       opts,
		"profit_type":   eType, "profit_value": eVal,
	})
}

// priceVal 周期价格字符串转非负浮点；非法/缺失按 0 处理。
func priceVal(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// productProfitType/productProfitValue 返回计价用利润方式/值：产品自身设置了利润则用
// 产品值，否则回退到其所属服务器默认利润（无则为 0）。各自独立判定，语义与旧实现一致。
func productProfitType(ctx context.Context, products *repo.Products, pType int16, pVal float64, productID int64) int16 {
	if pVal > 0 {
		return pType
	}
	t, _ := products.ServerProfitFallback(ctx, productID)
	return t
}

func productProfitValue(ctx context.Context, products *repo.Products, _ int16, pVal float64, productID int64) float64 {
	if pVal > 0 {
		return pVal
	}
	_, v := products.ServerProfitFallback(ctx, productID)
	return v
}

func (h *Pages) myServices(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	list, err := h.Svc.ListByUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "读取服务失败", 500)
		return
	}
	statusText := map[int16]string{0: "待开通", 1: "激活", 2: "已停机"}
	soon := time.Now().AddDate(0, 0, 14)
	for i := range list {
		svc := &list[i]
		svc.StatusText = statusText[svc.Status]
		svc.ExpiringSoon = svc.ExpiresAt.Before(soon)
		svc.DaysLeft = int(time.Until(svc.ExpiresAt).Hours() / 24)
		monthly, quarterly, yearly := priceVal(svc.MonthlyBase), priceVal(svc.QuarterlyBase), priceVal(svc.YearlyBase)
		cycles, defaultCycle := service.AvailableCycles(monthly, quarterly, yearly)
		svc.ShowMonthly = len(cycles) == 0 || monthly > 0
		svc.ShowQ = quarterly > 0
		svc.ShowY = yearly > 0
		svc.DefaultCycle = defaultCycle
		if svc.DefaultCycle == "" {
			svc.DefaultCycle = "monthly"
		}
		// 配置摘要 + 月价：主查询带回的数据在内存计算（不逐行查库）
		svc.ConfigDesc, svc.Monthly = rowPricing(svc.ConfigSnap, svc.ConfigOpts, svc.MonthlyBase,
			svc.ProfitType, svc.ProfitValue, svc.ServerProfitType, svc.ServerProfitValue)
		if svc.ConfigNote != "" { // 后台手工填写的配置说明优先
			svc.ConfigDesc = svc.ConfigNote
		}
	}
	// ponytail: 单 Pages 实例跨请求共用 8 个列表额度；多进程独立计数，需集群限流时改共享配额。
	h.serviceListOnce.Do(func() { h.serviceListSlots = make(chan struct{}, 8) })
	// 排队、取额度、查库和上游请求共用整个补全阶段的预算，失败保留安全快照投影。
	cctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer cancel()
	jobs := make(chan *service.ServiceRow)
	var wg sync.WaitGroup
	for range min(4, len(list)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for svc := range jobs {
				if cctx.Err() != nil {
					return
				}
				select {
				case h.serviceListSlots <- struct{}{}:
				case <-cctx.Done():
					return
				}
				// 同时就绪时 select 可能选中额度，调用前再次检查取消。
				if cctx.Err() != nil {
					<-h.serviceListSlots
					return
				}
				d, derr := h.Console.HostDetail(cctx, userID, svc.ID)
				<-h.serviceListSlots
				if derr == nil {
					svc.IP = d.IP
					if d.OSName != "" {
						svc.OS = d.OSName
						if d.OSVersion != "" {
							svc.OS += "-" + d.OSVersion
						}
					}
				}
			}
		}()
	}
dispatch:
	for i := range list {
		if cctx.Err() != nil {
			break
		}
		svc := &list[i]
		if !svc.HasUpstream || (svc.Status != 1 && svc.Status != 2) {
			continue
		}
		select {
		case jobs <- svc:
		case <-cctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		svc := list[i]
		out = append(out, map[string]any{
			"id": svc.ID, "name": svc.Name, "status": svc.Status, "status_text": svc.StatusText,
			"hostname": svc.Hostname, "ip": svc.IP, "os": svc.OS,
			"expires_at": svc.ExpiresAt.Format("2006-01-02 15:04"),
			"days_left":  svc.DaysLeft, "expiring_soon": svc.ExpiringSoon,
			"transition": svc.Transition,
			"product_id": svc.ProductID, "monthly": svc.Monthly,
			"config_desc": svc.ConfigDesc, "show_monthly": svc.ShowMonthly, "show_q": svc.ShowQ, "show_y": svc.ShowY,
			"default_cycle": svc.DefaultCycle,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// rowPricing 从列表行携带的快照/选项/利润数据计算配置摘要与月价（不查库）。
// 用户服务列表（myServices）与后台服务列表（ServicesList）共用同一口径。
func rowPricing(snap, optsJSON []byte, monthlyBase string, pt int16, pv float64, spt int16, spv float64) (desc, monthly string) {
	var sel map[string]string
	if len(snap) > 0 {
		var saved struct {
			Selection map[string]string `json:"selection"`
		}
		if json.Unmarshal(snap, &saved) == nil {
			sel = saved.Selection
		}
	}
	var opts []repo.ConfigOption
	_ = json.Unmarshal(optsJSON, &opts)
	desc = configDescFromOpts(opts, sel)
	if base, err := strconv.ParseFloat(monthlyBase, 64); err == nil {
		if pv <= 0 { // 产品未设利润回退服务器默认（与 ProductSellProfit 口径一致）
			pt, pv = spt, spv
		}
		monthly = fmt.Sprintf("%.2f", service.SellPriceFromData(base, opts, pt, pv, sel))
	}
	return desc, monthly
}

// configDescFromOpts 内存版配置摘要（opts 已随主查询带回，避免逐行查询）。
func configDescFromOpts(opts []repo.ConfigOption, selection map[string]string) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		if o.Hidden || o.Field == "os" {
			continue
		}
		val := strings.TrimSpace(selection[o.Field])
		if val == "" {
			continue
		}
		if o.Mode == "range" {
			parts = append(parts, fmt.Sprintf("%s %s%s", o.Name, val, o.Unit))
			continue
		}
		name := val
		for _, s := range o.Subs {
			if s.Value == val || s.Name == val {
				name = s.Name
				break
			}
		}
		parts = append(parts, fmt.Sprintf("%s %s", o.Name, name))
	}
	return strings.Join(parts, " · ")
}

func pathID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}
