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
	"lumeidc/internal/server"
)

//go:embed templates/admin.html templates/admin_*.html
var adminFS embed.FS

type AdminData struct {
	Page          string
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
	Providers     []server.ProviderInfo // 上游供应商清单（服务器表单下拉）
	// ProviderFieldsJSON 服务器表单动态凭据字段：{"fields":{code:[...]}, "values":{api_url:...}}。
	ProviderFieldsJSON template.JS
	// ProductHintsJSON 产品表单供应商差异声明：{code:{markupFree,hideCatalog,pidHint}}。
	ProductHintsJSON template.JS
	// ProviderWidgets 供应商产品表单独立区块（插槽，按当前供应商显隐）。
	ProviderWidgets template.HTML
	// GlobalProfit 全局默认利润（产品未单独设置时回退）
	GlobalProfitType  int64
	GlobalProfitValue float64
	// ServerProfit 服务器默认利润（导入表单预填）
	ServerProfitType  int16
	ServerProfitValue float64
}

func renderAdmin(w http.ResponseWriter, page string, data AdminData) {
	data.Page = page
	tpl, err := template.ParseFS(adminFS, "templates/admin.html", "templates/"+page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// 直接传结构体：此前手动拼 map 曾漏字段（Providers/Secret/TotalProfit 静默丢失），勿再回退。
	if err := tpl.ExecuteTemplate(w, "admin", data); err != nil {
		log.Printf("[template] %s: %v", page, err)
	}
}

type adminRow struct {
	ID         int64
	A, B, C, D string // 通用多列展示，避免为每张表写模板
	E          string // 扩展列（如订单利润）
	F          string // 支付实付/手续费等审计信息
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
		`SELECT o.id,coalesce(u.email,''),o.amount,coalesce(i.paid_amount,0)::text,coalesce(i.fee_amount,0)::text,o.cycle,CASE o.status WHEN 0 THEN '未支付' WHEN 1 THEN '已支付' ELSE '取消' END,coalesce(o.profit,'0')
		 FROM orders o JOIN users u ON u.id=o.user_id LEFT JOIN invoices i ON i.order_id=o.id ORDER BY o.id DESC LIMIT 50`)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	defer rows.Close()
	var out []adminRow
	var total float64
	for rows.Next() {
		var rw adminRow
		var profit, paidAmount, feeAmount string
		if err := rows.Scan(&rw.ID, &rw.A, &rw.B, &paidAmount, &feeAmount, &rw.C, &rw.D, &profit); err != nil {
			continue
		}
		rw.E = profit
		if paidAmount != "0" && paidAmount != "0.00" {
			rw.F = paidAmount + "（手续费 " + feeAmount + "）"
		}
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
	manualRequiresPhone := get("manual_identity_requires_verified_phone")
	if manualRequiresPhone == "" {
		manualRequiresPhone = "1"
	}
	cfg := map[string]string{
		"smtp_host":                                 get("smtp_host"),
		"smtp_port":                                 get("smtp_port"),
		"smtp_user":                                 get("smtp_user"),
		"smtp_pass":                                 get("smtp_pass"),
		"smtp_from":                                 get("smtp_from"),
		"notify_email_enabled":                      get("notify_email_enabled"),
		"sms_provider":                              get("sms_provider"),
		"sms_endpoint":                              get("sms_endpoint"),
		"sms_access_key":                            get("sms_access_key"),
		"sms_username":                              get("sms_username"),
		"sms_api_key":                               "",
		"sms_sign_name":                             get("sms_sign_name"),
		"sms_template_code":                         get("sms_template_code"),
		"captcha_provider":                          get("captcha_provider"),
		"captcha_geetest_id":                        get("captcha_geetest_id"),
		"captcha_vaptcha_vid":                       get("captcha_vaptcha_vid"),
		"captcha_corptcha_site_key":                 get("captcha_corptcha_site_key"),
		"verification_provider":                     get("verification_provider"),
		"verification_endpoint":                     get("verification_endpoint"),
		"verification_token":                        "",
		"verification_baidu_api_key":                get("verification_baidu_api_key"),
		"verification_baidu_plan_id":                get("verification_baidu_plan_id"),
		"verification_leaf_app_id":                  get("verification_leaf_app_id"),
		"verification_leaf_api_base":                get("verification_leaf_api_base"),
		"verification_smapi_api_url":                get("verification_smapi_api_url"),
		"verification_smapi_product_code":           get("verification_smapi_product_code"),
		"verification_stay33_api_url":               get("verification_stay33_api_url"),
		"verification_stay33_biz_code":              get("verification_stay33_biz_code"),
		"manual_identity_requires_verified_phone":   manualRequiresPhone,
		"manual_identity_enabled":                   get("manual_identity_enabled"),
		"registration_email_enabled":                get("registration_email_enabled"),
		"registration_phone_enabled":                get("registration_phone_enabled"),
		"registration_email_verification_required":  get("registration_email_verification_required"),
		"registration_phone_verification_required":  get("registration_phone_verification_required"),
		"registration_show_all_methods":             get("registration_show_all_methods"),
		"login_email_enabled":                       get("login_email_enabled"),
		"login_phone_enabled":                       get("login_phone_enabled"),
		"login_phone_otp_enabled":                   get("login_phone_otp_enabled"),
		"captcha_enabled":                           get("captcha_enabled"),
		"captcha_register_enabled":                  get("captcha_register_enabled"),
		"captcha_login_enabled":                     get("captcha_login_enabled"),
		"captcha_admin_login_enabled":               get("captcha_admin_login_enabled"),
		"captcha_email_code_enabled":                get("captcha_email_code_enabled"),
		"captcha_phone_code_enabled":                get("captcha_phone_code_enabled"),
		"captcha_password_reset_enabled":            get("captcha_password_reset_enabled"),
		"external_captcha_register_enabled":         get("external_captcha_register_enabled"),
		"external_captcha_login_enabled":            get("external_captcha_login_enabled"),
		"external_captcha_phone_login_code_enabled": get("external_captcha_phone_login_code_enabled"),
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s := &repo.Settings{DB: a.DB}
	set := func(k, v string) {
		if err := s.Set(r.Context(), k, v); err != nil {
			log.Printf("[settings] 保存 %s 失败: %v", k, err)
		}
	}
	section := r.PostFormValue("settings_section")
	setFlag := func(key string) {
		if r.PostFormValue(key) == "1" {
			set(key, "1")
		} else {
			set(key, "0")
		}
	}
	if section == "registration" {
		for _, key := range []string{"registration_email_enabled", "registration_phone_enabled", "registration_email_verification_required", "registration_phone_verification_required", "registration_show_all_methods", "login_phone_otp_enabled"} {
			setFlag(key)
		}
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}
	if section == "captcha" {
		for _, key := range []string{"captcha_enabled", "captcha_register_enabled", "captcha_login_enabled", "captcha_admin_login_enabled", "captcha_email_code_enabled", "captcha_phone_code_enabled", "captcha_password_reset_enabled"} {
			setFlag(key)
		}
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}
	if section == "sms" {
		set("sms_provider", strings.TrimSpace(r.PostFormValue("sms_provider")))
		set("sms_access_key", strings.TrimSpace(r.PostFormValue("sms_access_key")))
		set("sms_username", strings.TrimSpace(r.PostFormValue("sms_username")))
		set("sms_sign_name", strings.TrimSpace(r.PostFormValue("sms_sign_name")))
		set("sms_template_code", strings.TrimSpace(r.PostFormValue("sms_template_code")))
		set("sms_endpoint", strings.TrimSpace(r.PostFormValue("sms_endpoint")))
		if secret := strings.TrimSpace(r.PostFormValue("sms_secret_key")); secret != "" {
			set("sms_secret_key", secret)
			set("sms_token", secret)
		}
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}
	if section == "manual_identity" {
		setFlag("manual_identity_enabled")
		setFlag("manual_identity_requires_verified_phone")
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}
	if section == "external_captcha" {
		provider := strings.ToLower(strings.TrimSpace(r.PostFormValue("captcha_provider")))
		if provider != "" && provider != "geetest" && provider != "vaptcha" && provider != "corptcha" {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape("外部验证码 provider 无效"), http.StatusSeeOther)
			return
		}
		isOn := func(key string) bool { value, _ := s.Get(r.Context(), key); return value == "1" }
		localRegister := isOn("captcha_enabled") && isOn("captcha_register_enabled")
		localLogin := isOn("captcha_enabled") && isOn("captcha_login_enabled")
		localPhone := isOn("captcha_enabled") && isOn("captcha_phone_code_enabled")
		if (r.PostFormValue("external_captcha_register_enabled") == "1" && localRegister) || (r.PostFormValue("external_captcha_login_enabled") == "1" && localLogin) || (r.PostFormValue("external_captcha_phone_login_code_enabled") == "1" && localPhone) {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape("同一普通场景不能同时启用本地和外部验证码"), http.StatusSeeOther)
			return
		}
		set("captcha_provider", provider)
		set("captcha_geetest_id", strings.TrimSpace(r.PostFormValue("captcha_geetest_id")))
		set("captcha_vaptcha_vid", strings.TrimSpace(r.PostFormValue("captcha_vaptcha_vid")))
		set("captcha_corptcha_site_key", strings.TrimSpace(r.PostFormValue("captcha_corptcha_site_key")))
		for _, key := range []string{"captcha_geetest_key", "captcha_vaptcha_key", "captcha_corptcha_secret"} {
			if value := strings.TrimSpace(r.PostFormValue(key)); value != "" {
				set(key, value)
			}
		}
		for _, key := range []string{"external_captcha_register_enabled", "external_captcha_login_enabled", "external_captcha_phone_login_code_enabled"} {
			setFlag(key)
		}
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}

	// 兼容旧版未带 settings_section 的 SMTP/综合表单。
	set("smtp_host", strings.TrimSpace(r.PostFormValue("smtp_host")))
	set("smtp_port", strings.TrimSpace(r.PostFormValue("smtp_port")))
	set("smtp_user", strings.TrimSpace(r.PostFormValue("smtp_user")))
	if pass := r.PostFormValue("smtp_pass"); pass != "" {
		set("smtp_pass", pass)
	}
	set("smtp_from", strings.TrimSpace(r.PostFormValue("smtp_from")))
	setFlag("notify_email_enabled")
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
