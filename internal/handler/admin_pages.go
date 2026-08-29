package handler

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

//go:embed templates/admin.html templates/admin_*.html
var adminFS embed.FS

type AdminData struct {
	Rows          any
	Error         string
	Msg           string
	CSRF          string
	Product       any
	Types         any
	Monthly       string
	Quarterly     string
	Yearly        string
	ConfigJSON    string
	ServersList   any
	UpstreamBound bool
	Secret        string
	TotalProfit   string
}

func renderAdmin(w http.ResponseWriter, page string, data AdminData) {
	tpl, err := template.ParseFS(adminFS, "templates/admin.html", "templates/"+page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	m := map[string]any{"CSRF": data.CSRF, "Error": data.Error, "Msg": data.Msg,
		"Product": data.Product, "Types": data.Types,
		"Monthly": data.Monthly, "Quarterly": data.Quarterly, "Yearly": data.Yearly,
		"ConfigJSON": data.ConfigJSON, "ServersList": data.ServersList,
		"UpstreamBound": data.UpstreamBound}
	if data.Rows != nil {
		m["Rows"] = data.Rows
	}
	// 模板执行错误必须记录：否则 range 内字段错误会导致响应静默截断，极难排查
	if err := tpl.ExecuteTemplate(w, "admin", m); err != nil {
		log.Printf("[template] %s: %v", page, err)
	}
}

type adminRow struct {
	ID         int64
	A, B, C, D string // 通用多列展示，避免为每张表写模板
	E          string // 扩展列（如订单利润）
}

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	var users, orders, services int64
	_ = a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM users`).Scan(&users)
	_ = a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM orders WHERE status=1`).Scan(&orders)
	_ = a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM services WHERE status=1`).Scan(&services)
	rows := []adminRow{
		{ID: 1, A: "用户数", B: itoa(users)},
		{ID: 2, A: "已支付订单", B: itoa(orders)},
		{ID: 3, A: "激活服务", B: itoa(services)},
	}
	renderAdmin(w, "admin_dashboard.html", AdminData{Rows: rows})
}

func (a *Admin) adminOrders(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(),
		`SELECT o.id,u.email,o.amount,o.cycle,CASE o.status WHEN 0 THEN '未支付' WHEN 1 THEN '已支付' ELSE '取消' END,coalesce(o.profit,'0')
		 FROM orders o JOIN users u ON u.id=o.user_id ORDER BY o.id DESC LIMIT 50`)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	defer rows.Close()
	var out []adminRow
	var total float64
	for rows.Next() {
		var rw adminRow
		var profit string
		if err := rows.Scan(&rw.ID, &rw.A, &rw.B, &rw.C, &rw.D, &profit); err != nil {
			continue
		}
		rw.E = profit
		if pf, perr := strconv.ParseFloat(profit, 64); perr == nil {
			total += pf
		}
		out = append(out, rw)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	renderAdmin(w, "admin_orders.html", AdminData{
		Rows:        out,
		CSRF:        csrfOf(adminSessions, w, r),
		Error:       r.URL.Query().Get("err"),
		TotalProfit: strconv.FormatFloat(total, 'f', 2, 64),
	})
}

func (a *Admin) require(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

// adminLogs GET /admin/logs — 操作审计日志。
func (a *Admin) adminLogs(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	logs, err := (&repo.AdminLog{DB: a.DB}).List(r.Context(), 100)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	renderAdmin(w, "admin_logs.html", AdminData{Rows: logs})
}

// adminRefunds GET /admin/refunds — 退款记录列表。
func (a *Admin) adminRefunds(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := (&repo.Refunds{DB: a.DB}).List(r.Context(), 100)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	renderAdmin(w, "admin_refunds.html", AdminData{Rows: rows, Msg: r.URL.Query().Get("ok")})
}

// adminAnnouncements GET /admin/announcements — 公告列表。
func (a *Admin) adminAnnouncements(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := a.Announcements.List(r.Context(), false)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	renderAdmin(w, "admin_announcements.html", AdminData{
		Rows:  rows,
		CSRF:  csrfOf(adminSessions, w, r),
		Error: r.URL.Query().Get("err"),
		Msg:   r.URL.Query().Get("msg"),
	})
}

// adminAnnouncementForm GET /admin/announcements/edit?id= — 新建/编辑表单。
func (a *Admin) adminAnnouncementForm(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	data := AdminData{CSRF: csrfOf(adminSessions, w, r)}
	if id := r.URL.Query().Get("id"); id != "" {
		if pid, err := strconv.ParseInt(id, 10, 64); err == nil {
			// 仅成功时赋值：把类型化 nil 指针塞进 any 会让模板 {{if .Product}} 判空失效。
			if an, gerr := a.Announcements.Get(r.Context(), pid); gerr == nil {
				data.Product = an
			}
		}
	}
	renderAdmin(w, "admin_announcement_form.html", data)
}

// adminAnnouncementSave POST /admin/announcements/save — 保存（新增/更新）。
func (a *Admin) adminAnnouncementSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	title := strings.TrimSpace(r.PostFormValue("title"))
	content := r.PostFormValue("content")
	hidden := r.PostFormValue("hidden") != ""
	pinned := r.PostFormValue("pinned") != ""
	if title == "" {
		http.Redirect(w, r, "/admin/announcements?err=标题不能为空", http.StatusSeeOther)
		return
	}
	if err := a.Announcements.Save(r.Context(), id, title, content, hidden, pinned); err != nil {
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/announcements?msg=已保存", http.StatusSeeOther)
}

// adminAnnouncementDelete POST /admin/announcements/{id}/delete — 删除。
func (a *Admin) adminAnnouncementDelete(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Announcements.Delete(r.Context(), id); err != nil {
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/announcements?msg=已删除", http.StatusSeeOther)
}

// adminCoupons GET /admin/coupons — 优惠码列表 + 新建表单。
func (a *Admin) adminCoupons(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	list, err := (&repo.Coupons{DB: a.DB}).List(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	renderAdmin(w, "admin_coupons.html", AdminData{
		Rows:  list,
		CSRF:  csrfOf(adminSessions, w, r),
		Error: r.URL.Query().Get("err"),
		Msg:   r.URL.Query().Get("ok"),
	})
}

// adminSettings GET /admin/settings — 通知/SMTP 配置。
func (a *Admin) adminSettings(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	s := &repo.Settings{DB: a.DB}
	get := func(k string) string {
		v, _ := s.Get(r.Context(), k)
		return v
	}
	cfg := map[string]string{
		"smtp_host":            get("smtp_host"),
		"smtp_port":            get("smtp_port"),
		"smtp_user":            get("smtp_user"),
		"smtp_pass":            get("smtp_pass"),
		"smtp_from":            get("smtp_from"),
		"notify_email_enabled": get("notify_email_enabled"),
	}
	renderAdmin(w, "admin_settings.html", AdminData{
		CSRF:        csrfOf(adminSessions, w, r),
		Error:       r.URL.Query().Get("err"),
		Msg:         r.URL.Query().Get("ok"),
		ServersList: cfg,
	})
}

// adminSettingsSave POST /admin/settings — 保存 SMTP 配置。
func (a *Admin) adminSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	s := &repo.Settings{DB: a.DB}
	set := func(k, v string) {
		if err := s.Set(r.Context(), k, v); err != nil {
			log.Printf("[settings] 保存 %s 失败: %v", k, err)
		}
	}
	set("smtp_host", strings.TrimSpace(r.PostFormValue("smtp_host")))
	set("smtp_port", strings.TrimSpace(r.PostFormValue("smtp_port")))
	set("smtp_user", strings.TrimSpace(r.PostFormValue("smtp_user")))
	// 密码框浏览器不回显；留空表示沿用已保存的密码，避免误清空导致发信失败。
	if p := r.PostFormValue("smtp_pass"); p != "" {
		set("smtp_pass", p)
	}
	set("smtp_from", strings.TrimSpace(r.PostFormValue("smtp_from")))
	en := "0"
	if r.PostFormValue("notify_email_enabled") == "1" {
		en = "1"
	}
	set("notify_email_enabled", en)
	http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
}

// adminCouponCreate POST /admin/coupons/save — 新建优惠码。
func (a *Admin) adminCouponCreate(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	typ := r.PostFormValue("type")
	value, _ := strconv.ParseFloat(r.PostFormValue("value"), 64)
	minA, _ := strconv.ParseFloat(r.PostFormValue("min_amount"), 64)
	limit, _ := strconv.Atoi(r.PostFormValue("usage_limit"))
	var expires *time.Time
	if es := strings.TrimSpace(r.PostFormValue("expires_at")); es != "" {
		if t, err := time.Parse("2006-01-02", es); err == nil {
			expires = &t
		}
	}
	if err := (&repo.Coupons{DB: a.DB}).Create(r.Context(), code, typ, value, minA, limit, expires); err != nil {
		http.Redirect(w, r, "/admin/coupons?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
