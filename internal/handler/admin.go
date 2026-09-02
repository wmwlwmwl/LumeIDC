package handler

import (
	"database/sql"
	"net/http"
	"net/url"
	"time"

	"lumeidc/internal/captcha"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/totp"
)

type Admin struct {
	Admins        *repo.Admins
	DB            *sql.DB
	Lockout       *repo.LoginAttempts
	Announcements *repo.Announcements
	LocalCaptcha  *captcha.Service
}

// GwRepo 网关配置存储；AdminPages 用。
var _ = 0

func (a *Admin) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", a.loginForm)
	mux.HandleFunc("POST /admin/login", a.loginSubmit)
	mux.HandleFunc("GET /admin", a.dashboard)
	mux.HandleFunc("GET /admin/orders", a.adminOrders)
	mux.HandleFunc("GET /admin/logs", a.adminLogs)
	mux.HandleFunc("GET /admin/refunds", a.adminRefunds)
	mux.HandleFunc("GET /admin/announcements", a.adminAnnouncements)
	mux.HandleFunc("GET /admin/announcements/edit", a.adminAnnouncementForm)
	mux.HandleFunc("POST /admin/announcements/save", a.adminAnnouncementSave)
	mux.HandleFunc("POST /admin/announcements/{id}/delete", a.adminAnnouncementDelete)
	mux.HandleFunc("GET /admin/coupons", a.adminCoupons)
	mux.HandleFunc("POST /admin/coupons/save", a.adminCouponCreate)
	mux.HandleFunc("GET /admin/settings", a.adminSettings)
	mux.HandleFunc("POST /admin/settings", a.adminSettingsSave)
	mux.HandleFunc("GET /admin/totp", a.totpSetup)
	mux.HandleFunc("POST /admin/totp", a.totpSetupPost)
	mux.HandleFunc("GET /admin/password", a.passwordForm)
	mux.HandleFunc("POST /admin/password", a.passwordSubmit)
}

func (a *Admin) loginForm(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CSRF": csrfOf(adminSessions, w, r)}
	if a.LocalCaptcha != nil {
		data["CaptchaAdminLoginEnabled"] = a.LocalCaptcha.Enabled(r.Context(), "admin_login")
	}
	renderAuth(w, data)
}

func (a *Admin) captchaCheck(r *http.Request) error {
	if a.LocalCaptcha == nil || !a.LocalCaptcha.Enabled(r.Context(), "admin_login") {
		return nil
	}
	return a.LocalCaptcha.Verify(r.Context(), "admin_login", r.PostFormValue("captcha_id"), r.PostFormValue("captcha_answer"), requestIP(r))
}

// adminLoginLabel 区分管理员登录表单文案：管理员用用户名而非邮箱。

// adminSessions 是管理员登录用的独立 session 存储占位；
// ponytail: 一期与用户共用 Store 实例，用 IsAdmin 区分；二期拆分独立 cookie 域。
var adminSessions *middleware.Store

func SetAdminStore(s *middleware.Store) { adminSessions = s }

func (a *Admin) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if adminSessions == nil {
		http.Error(w, "服务器内部错误", 500)
		return
	}
	if err := a.captchaCheck(r); err != nil {
		http.Error(w, "请完成图形验证码后再登录", http.StatusUnauthorized)
		return
	}
	email := r.PostFormValue("email")
	// 登录锁定：达阈值后临时拒绝，阻断爆破。
	if a.Lockout != nil {
		if locked, lerr := a.Lockout.Locked(r.Context(), email); lerr == nil && locked {
			w.WriteHeader(http.StatusTooManyRequests)
			renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": true, "CSRF": csrfOf(adminSessions, w, r), "Error": "尝试次数过多，账户已临时锁定，请 15 分钟后再试"})
			return
		}
	}
	id, err := a.Admins.Verify(r.Context(), email, r.PostFormValue("password"))
	if err != nil {
		if a.Lockout != nil {
			_ = a.Lockout.Fail(r.Context(), email)
		}
		w.WriteHeader(http.StatusUnauthorized)
		renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": true, "CSRF": csrfOf(adminSessions, w, r), "Error": "用户名或密码错误"})
		return
	}
	// 两步验证（若已启用）
	if a.Admins != nil {
		if secret, enabled, gerr := a.Admins.GetTOTP(r.Context(), id); gerr == nil && enabled && secret != "" {
			if !totp.Verify(secret, r.PostFormValue("totp"), time.Now()) {
				if a.Lockout != nil {
					_ = a.Lockout.Fail(r.Context(), email)
				}
				w.WriteHeader(http.StatusUnauthorized)
				renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": true, "CSRF": csrfOf(adminSessions, w, r), "Error": "两步验证码错误"})
				return
			}
		}
	}
	if a.Lockout != nil {
		_ = a.Lockout.Clear(r.Context(), email)
	}
	sess := adminSessions.Start(w)
	sess.IsAdmin = true
	sess.UserID = id
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// passwordForm GET /admin/password — 管理员修改自己密码。
func (a *Admin) passwordForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
		return
	}
	renderAdmin(w, "admin_password.html", AdminData{
		CSRF:  csrfOf(adminSessions, w, r),
		Error: r.URL.Query().Get("err"),
		Msg:   r.URL.Query().Get("ok"),
	})
}

// passwordSubmit POST /admin/password — 验证旧密码后更新。
func (a *Admin) passwordSubmit(w http.ResponseWriter, r *http.Request) {
	sess, ok := middleware.RequireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/password?err=表单解析失败", http.StatusSeeOther)
		return
	}
	err := a.Admins.ChangePassword(r.Context(), sess.UserID,
		r.PostFormValue("old_password"), r.PostFormValue("new_password"))
	if err != nil {
		msg := "修改失败"
		switch {
		case repo.IsWrongOldPassword(err):
			msg = "旧密码错误"
		case repo.IsPasswordTooShort(err):
			msg = "新密码至少8位"
		}
		http.Redirect(w, r, "/admin/password?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	if adminSessions != nil {
		adminSessions.RevokeAdmin(sess.UserID)
	}
	http.Redirect(w, r, "/admin/login?ok="+url.QueryEscape("密码已修改，请重新登录"), http.StatusSeeOther)
}

// totpSetup GET /admin/totp — 两步验证设置页（生成/展示密钥，确认后启用）。
func (a *Admin) totpSetup(w http.ResponseWriter, r *http.Request) {
	sess, ok := middleware.RequireAdmin(w, r)
	if !ok {
		return
	}
	secret, enabled, _ := a.Admins.GetTOTP(r.Context(), sess.UserID)
	if !enabled && secret == "" {
		if s, e := totp.NewSecret(); e == nil {
			_ = a.Admins.SetTOTP(r.Context(), sess.UserID, s)
			secret = s
		}
	}
	uri := totp.URI("LumeIDC-Admin", "LumeIDC", secret)
	renderAdmin(w, "admin_totp.html", AdminData{
		CSRF:          csrfOf(adminSessions, w, r),
		Secret:        secret,
		UpstreamBound: enabled,
		Msg:           r.URL.Query().Get("ok"),
		Error:         r.URL.Query().Get("err"),
		ServersList:   map[string]any{"URI": uri},
	})
}

// totpSetupPost POST /admin/totp — 启用（校验码）或关闭两步验证。
func (a *Admin) totpSetupPost(w http.ResponseWriter, r *http.Request) {
	sess, ok := middleware.RequireAdmin(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("action") == "disable" {
		_ = a.Admins.DisableTOTP(r.Context(), sess.UserID)
		http.Redirect(w, r, "/admin/totp?ok=1", http.StatusSeeOther)
		return
	}
	secret, _, _ := a.Admins.GetTOTP(r.Context(), sess.UserID)
	if !totp.Verify(secret, r.PostFormValue("code"), time.Now()) {
		http.Redirect(w, r, "/admin/totp?err="+url.QueryEscape("验证码错误"), http.StatusSeeOther)
		return
	}
	if err := a.Admins.EnableTOTP(r.Context(), sess.UserID); err != nil {
		http.Redirect(w, r, "/admin/totp?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/totp?ok=1", http.StatusSeeOther)
}
