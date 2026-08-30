package handler

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

//go:embed templates/site.html templates/products.html templates/service_list.html templates/buy.html
//go:embed templates/user_home.html templates/user_invoices.html templates/user_password.html templates/service_detail.html
//go:embed templates/user_notifications.html
var siteFS embed.FS

type Pages struct {
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
}

// sessionsStore 由 httpserver 注入，用于匿名会话 CSRF token。
var sessionsStore *middleware.Store

func SetPageStore(s *middleware.Store) { sessionsStore = s }

func (h *Pages) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /products", h.products)
	mux.HandleFunc("GET /services", h.myServices)
	mux.HandleFunc("GET /buy/{productID}", h.buyForm)
	mux.HandleFunc("GET /user", h.userHome)
	mux.HandleFunc("GET /user/invoices", h.userInvoices)
	mux.HandleFunc("GET /services/{serviceID}", h.serviceDetail)
	mux.HandleFunc("POST /services/{serviceID}/renew", h.serviceRenew)
	mux.HandleFunc("POST /services/{serviceID}/cancel", h.serviceCancel)
	mux.HandleFunc("POST /services/{serviceID}/console", h.consoleAction)
	mux.HandleFunc("GET /services/{serviceID}/console", h.consoleAction)
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
	mux.HandleFunc("GET /services/{serviceID}/vnc-assets/{path...}", h.vncAssets)
	mux.HandleFunc("GET /services/{serviceID}/vnc-ws", h.vncWebSocket)
	mux.HandleFunc("GET /services/{serviceID}/vnc-pass", h.serviceVncPass)
	mux.HandleFunc("GET /services/{serviceID}/reinstall-options", h.reinstallOptions)
	mux.HandleFunc("GET /services/{serviceID}/module", h.serviceModuleOverview)
	mux.HandleFunc("GET /services/{serviceID}/module/{key}", h.serviceModulePage)
	mux.HandleFunc("POST /services/{serviceID}/module/{key}", h.serviceModuleSubmit)
	mux.HandleFunc("GET /services/{serviceID}/module-assets/{host64}/{path...}", h.serviceModuleAssets)
	mux.HandleFunc("GET /user/password", h.passwordForm)
	mux.HandleFunc("POST /user/password", h.passwordSubmit)
	mux.HandleFunc("GET /notifications", h.notifications)
}

// notifications GET /notifications — 站内信列表（读取后标记已读）。
func (h *Pages) notifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	list := []map[string]any{}
	if h.Notifier != nil {
		if l, err := h.Notifier.List(r.Context(), userID); err == nil {
			list = l
		}
		h.Notifier.MarkRead(r.Context(), userID)
	}
	render(w, r, "user_notifications.html", map[string]any{"Rows": list})
}

// render 用 site.html 作为布局渲染子页。balanceRepo 由 httpserver 注入用于导航栏余额显示。
var balanceRepo *repo.Balance

func SetBalanceRepo(b *repo.Balance) { balanceRepo = b }

func render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	tpl, err := template.ParseFS(siteFS, "templates/site.html", "templates/"+page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Page"] = page
	if sess := middleware.FromSession(r.Context()); sess != nil {
		data["CSRF"] = sess.CSRFToken()
		if sess.UserID > 0 && !sess.IsAdmin {
			data["LoggedIn"] = true
			if bal, err := balanceRepo.Get(r.Context(), sess.UserID); err == nil {
				data["Balance"] = bal
			}
		}
	}
	if err := tpl.ExecuteTemplate(w, "site", data); err != nil {
		log.Printf("[template] %s: %v", page, err)
	}
}

// listAnnouncements 取前台展示的公告（显示中、置顶优先）。
func (h *Pages) listAnnouncements(ctx context.Context) []map[string]any {
	if h.Announcements == nil {
		return nil
	}
	list, err := h.Announcements.List(ctx, true)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, an := range list {
		out = append(out, map[string]any{
			"ID": an.ID, "Title": an.Title, "Content": an.Content,
			"Pinned": an.Pinned, "CreatedAt": an.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

type productView struct {
	ID      int64
	Name    string
	Desc    string
	Monthly string
	Stock   int
}

func (h *Pages) home(w http.ResponseWriter, r *http.Request) {
	h.productListPage(w, r, "/")
}

func (h *Pages) products(w http.ResponseWriter, r *http.Request) {
	h.productListPage(w, r, "/products")
}

func (h *Pages) productListPage(w http.ResponseWriter, r *http.Request, _ string) {
	types, _ := h.Products.ListTypes(r.Context())
	var list []repo.Product
	var err error
	gid := r.URL.Query().Get("gid")
	if gid == "" {
		list, err = h.Products.ListVisible(r.Context())
	} else if id, perr := strconv.ParseInt(gid, 10, 64); perr == nil {
		list, _ = h.Products.ListByType(r.Context(), id)
	} else {
		list, _ = h.Products.ListVisible(r.Context())
	}
	if err != nil && list == nil {
		http.Error(w, "读取产品失败", 500)
		return
	}
	psID, _ := h.Products.DefaultPricesetID(r.Context())
	var views []productView
	for _, p := range list {
		m := "-"
		if pr, err := h.Products.Price(r.Context(), p.ID, psID); err == nil {
			opts, _ := h.Products.GetConfigOptions(r.Context(), p.ID)
			eType, eVal := resolveProfitType(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID), resolveProfitValue(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID)
			m = fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(pr.Monthly), opts, eType, eVal))
		}
		views = append(views, productView{ID: p.ID, Name: p.Name, Desc: p.Description, Monthly: m, Stock: p.Stock})
	}
	render(w, r, "products.html", map[string]any{
		"Products": views, "Types": types, "GID": gid,
		"Announcements": h.listAnnouncements(r.Context()),
	})
}

func (h *Pages) buyForm(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "productID")
	p, err := h.Products.Get(r.Context(), id)
	if err != nil || p.Hidden {
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
	// 周期是否开售：价格>0 才展示（如上游仅开月付的配置计价产品，季/年付为 0 则隐藏）
	showQ := priceVal(pr.Quarterly) > 0
	showY := priceVal(pr.Yearly) > 0
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
	cfgJSON, _ := json.Marshal(map[string]any{
		"base": baseMap, "options": opts,
		"profit_type": resolveProfitType(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID),
		"profit_value": resolveProfitValue(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID),
	})
	// 周期下拉展示价：配置计价型（基础价 0）用加成后起步价，普通产品直接加成基础价。
	eType, eVal := resolveProfitType(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID), resolveProfitValue(r.Context(), p.ProfitType, p.ProfitValue, h.Products.DB, p.ID)
	dispMonthly := fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(pr.Monthly), opts, eType, eVal))
	dispQuarterly := fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(pr.Quarterly), opts, eType, eVal))
	dispYearly := fmt.Sprintf("%.2f", service.DisplayPrice(priceVal(pr.Yearly), opts, eType, eVal))
	render(w, r, "buy.html", map[string]any{
		"Product": p, "Monthly": dispMonthly, "Quarterly": dispQuarterly, "Yearly": dispYearly,
		"ShowQuarterly": showQ, "ShowYearly": showY,
		"CSRF": csrfOf(sessionsStore, w, r), "Options": opts,
		"ConfigData": template.JS(cfgJSON), "LoggedIn": isLoggedIn(r),
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

// resolveProfitType/resolveProfitValue 回退利润：产品利润为空时用服务器默认值。
func resolveProfitType(ctx context.Context, pType int16, pVal float64, db *sql.DB, productID int64) int16 {
	if pVal > 0 {
		return pType
	}
	var sid sql.NullInt64
	db.QueryRowContext(ctx, `SELECT server_id FROM products WHERE id=$1`, productID).Scan(&sid)
	if sid.Valid {
		var st int16
		if db.QueryRowContext(ctx, `SELECT coalesce(profit_type,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&st) == nil && st > 0 {
			return st
		}
	}
	return 0
}

func resolveProfitValue(ctx context.Context, pType int16, pVal float64, db *sql.DB, productID int64) float64 {
	if pVal > 0 {
		return pVal
	}
	var sid sql.NullInt64
	db.QueryRowContext(ctx, `SELECT server_id FROM products WHERE id=$1`, productID).Scan(&sid)
	if sid.Valid {
		var sv float64
		if db.QueryRowContext(ctx, `SELECT coalesce(profit_value,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&sv) == nil && sv > 0 {
			return sv
		}
	}
	return 0
}

func isLoggedIn(r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	return sess != nil && sess.UserID > 0 && !sess.IsAdmin
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
	psID, _ := h.Products.DefaultPricesetID(r.Context())
	soon := time.Now().AddDate(0, 0, 14)
	for i := range list {
		list[i].StatusText = statusText[list[i].Status]
		list[i].ExpiringSoon = list[i].ExpiresAt.Before(soon)
		if pr, err := h.Products.Price(r.Context(), list[i].ProductID, psID); err == nil {
			list[i].ShowQ = priceVal(pr.Quarterly) > 0
			list[i].ShowY = priceVal(pr.Yearly) > 0
		}
	}
	render(w, r, "service_list.html", map[string]any{
		"Services": list, "CSRF": csrfOf(sessionsStore, w, r)})
}

func pathID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}
