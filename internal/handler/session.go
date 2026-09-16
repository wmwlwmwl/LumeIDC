package handler

import (
	"net/http"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

// Session GET /session — SPA 启动端：启动（或复用）匿名会话，返回
// CSRF 令牌、站点品牌与登录态，供前端 mount 前决定请求头、路由守卫与后台基址。
type Session struct {
	Users *repo.Users
	*Deps
}

func (h *Session) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /session", h.get)
}

func (h *Session) get(w http.ResponseWriter, r *http.Request) {
	csrf := csrfOf(h.PageStore, w, r) // 无会话时启动匿名会话并 Set-Cookie
	si := h.currentSiteInfo()

	out := map[string]any{
		"csrf": csrf,
		"site": map[string]any{
			"name":        si.Name,
			"description": si.Description,
			"keywords":    si.Keywords,
			"email":       si.ServiceEmail,
			"phone":       si.ServicePhone,
			"hours":       si.ServiceHours,
			"contacts":    si.ServiceContacts,
		},
		"user":  nil, // 匿名会话返回 null，前端统一判空
		"admin": h.adminBlock(r),
		// 认证配置：前端据此决定登录/注册页要展示哪些方式（验证码/邮箱验证码/手机验证码登录）。
		"auth": h.authFlags(r),
	}

	if sess := middleware.FromSession(r.Context()); sess != nil && sess.UserID > 0 {
		user := map[string]any{"id": sess.UserID, "isAdmin": sess.IsAdmin}
		if h.Users != nil {
			if u, err := h.Users.AdminUserByID(r.Context(), sess.UserID); err == nil {
				user["email"] = u.Email
				user["name"] = u.Name
				user["phone"] = u.Phone
			}
		}
		if h.Balance != nil {
			if bal, err := h.Balance.Get(r.Context(), sess.UserID); err == nil {
				user["balance"] = bal
			}
		}
		out["user"] = user
	}

	writeJSON(w, out)
}

// adminBlock 组装管理员通道信息：身份始终返回（仅反映本浏览器 cookie 状态），
// 自定义路径只对开发代理请求下发（见上方注释）。
func (h *Session) adminBlock(r *http.Request) map[string]any {
	// 路径按需下发：自定义后台路径是隐藏入口，不能经前台 /session 泄漏给匿名探测者
	// （curl /session 即可枚举）。生产环境后台 SPA 从自身 location 推导基址——
	// 能打开后台登录页的人必然已知道路径；开发期由 Vite 代理头放行保持旧行为。
	adminPath := "/admin"
	if h.AdminPathCfg != nil {
		if v := h.AdminPathCfg.Get(); v != "" {
			adminPath = v
		}
	}
	if adminPath != "/admin" && !middleware.IsDevProxy(r) {
		adminPath = ""
	}
	admin := map[string]any{"path": adminPath, "user": nil}
	if h.AdminStore != nil {
		if as := h.AdminStore.GetAdmin(r); as != nil && as.UserID > 0 {
			admin["user"] = map[string]any{"id": as.UserID, "isAdmin": true}
		}
	}
	return admin
}

// authFlags 汇总认证相关开关（登录/注册页据此决定展示哪些方式）。
// 读不到设置时按关闭处理（前端回退到最简形态）。
// 用 GetMany 一次读齐：此前逐键 Get，每个请求 12 条 SQL（该端点由 SPA 每次启动调用）。
func (h *Session) authFlags(r *http.Request) map[string]any {
	vals := map[string]string{}
	if h.Settings != nil {
		if m, err := h.Settings.GetMany(r.Context(),
			"login_phone_otp_enabled",
			"registration_email_enabled",
			"registration_phone_enabled",
			"registration_email_verification_required",
			"registration_phone_verification_required",
			"registration_require_both",
			"profile_change_require_old_email",
			"profile_change_require_old_phone",
			"captcha_enabled",
			"captcha_register_enabled",
			"captcha_login_enabled",
			"external_captcha_login_enabled",
			"external_captcha_register_enabled",
			"external_captcha_register_code_enabled",
			"external_captcha_forgot_code_enabled",
			"external_captcha_profile_code_enabled",
			"external_captcha_phone_login_code_enabled",
		); err == nil {
			vals = m
		}
	}
	on := func(key string, fallback bool) bool {
		if v, ok := vals[key]; ok && v != "" {
			return v == "1"
		}
		return fallback
	}
	return map[string]any{
		"phone_otp_login":              on("login_phone_otp_enabled", false),
		"email_registration":           on("registration_email_enabled", true),
		"phone_registration":           on("registration_phone_enabled", false),
		"email_verification_required":  on("registration_email_verification_required", false),
		"phone_verification_required":  on("registration_phone_verification_required", true),
		"require_both_registration":    on("registration_require_both", false),
		"require_old_email_change":     on("profile_change_require_old_email", true),
		"require_old_phone_change":     on("profile_change_require_old_phone", true),
		"captcha_register":             on("captcha_enabled", false) && on("captcha_register_enabled", false),
		"captcha_login":                on("captcha_enabled", false) && on("captcha_login_enabled", false),
		"external_captcha_login":       on("external_captcha_login_enabled", false),
		"external_captcha_register":    on("external_captcha_register_enabled", false),
		"register_code_external":       on("external_captcha_register_code_enabled", false),
		"forgot_code_external":         on("external_captcha_forgot_code_enabled", false),
		"profile_code_external":        on("external_captcha_profile_code_enabled", false),
		"external_captcha_phone_login": on("external_captcha_phone_login_code_enabled", false),
	}
}
