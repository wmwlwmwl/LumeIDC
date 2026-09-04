package handler

import (
	"net/http"
	"net/url"
	"time"

	"lumeidc/internal/captcha"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
	"lumeidc/internal/totp"
	"lumeidc/internal/update"
)

type Admin struct {
	Admins        *repo.Admins
	Lockout       *repo.LoginAttempts
	Announcements *repo.Announcements
	LocalCaptcha  *captcha.Service
	Coupons       *repo.Coupons
	Refunds       *repo.Refunds
	AdminLog      *repo.AdminLog
	Stats         *repo.Stats
	Notifier      *service.Notifier
	Updater       *update.Client // 系统在线更新（nil 时页面提示未启用）
	// ListenSwitcher 热切换监听端口（后台站点设置调用；nil 时仅保存不切换）。
	ListenSwitcher func(addr string) error
	// DefaultListen config.yaml 的 listen；后台端口留空时回退到该值。
	DefaultListen string
	*Deps
}

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
	mux.HandleFunc("POST /admin/settings/test-email", a.adminTestEmail)
	mux.HandleFunc("GET /admin/site", a.adminSite)
	mux.HandleFunc("POST /admin/site", a.adminSiteSave)
	mux.HandleFunc("GET /admin/totp", a.totpSetup)
	mux.HandleFunc("POST /admin/totp", a.totpSetupPost)
	mux.HandleFunc("GET /admin/password", a.passwordForm)
	mux.HandleFunc("POST /admin/password", a.passwordSubmit)
	mux.HandleFunc("GET /admin/update", a.adminUpdatePage)
	mux.HandleFunc("POST /admin/update/check", a.adminUpdateCheck)
	mux.HandleFunc("POST /admin/update/apply", a.adminUpdateApply)
	mux.HandleFunc("POST /admin/update/restart", a.adminUpdateRestart)
}

func (a *Admin) loginForm(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CSRF": a.adminCSRF(w, r),
		"CaptchaAdminLoginEnabled": a.adminCaptchaRequired(r)}
	a.renderAuth(w, data)
}

// adminLoginFailKey 管理员登录失败计数的 IP key（独立于账号锁定 key）。
func adminLoginFailKey(ip string) string { return "aip:" + ip }

// adminCaptchaRequired 管理员登录是否需要图形验证码：
// 后台已启用图形验证码，或该 IP 近期登录失败过（失败后强制出验证码防爆破）。
func (a *Admin) adminCaptchaRequired(r *http.Request) bool {
	if a.LocalCaptcha == nil {
		return false
	}
	if a.LocalCaptcha.Enabled(r.Context(), "admin_login") {
		return true
	}
	if a.Lockout != nil {
		if n, err := a.Lockout.Fails(r.Context(), adminLoginFailKey(requestIP(r))); err == nil && n > 0 {
			return true
		}
	}
	return false
}

func (a *Admin) captchaCheck(r *http.Request) error {
	if a.LocalCaptcha == nil || !a.adminCaptchaRequired(r) {
		return nil
	}
	// VerifyForced：失败后强制校验，不因后台开关关闭而跳过。
	return a.LocalCaptcha.VerifyForced(r.Context(), "admin_login", r.PostFormValue("captcha_id"), r.PostFormValue("captcha_answer"), requestIP(r))
}

func (a *Admin) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if a.AdminStore == nil {
		http.Error(w, "服务器内部错误", 500)
		return
	}
	if err := a.captchaCheck(r); err != nil {
		if a.Lockout != nil {
			_ = a.Lockout.Fail(r.Context(), adminLoginFailKey(requestIP(r))) // 答错也计数，防看图爆破
		}
		w.WriteHeader(http.StatusUnauthorized)
		a.renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": a.adminCaptchaRequired(r), "CSRF": a.adminCSRF(w, r), "Error": "图形验证码错误，请重试"})
		return
	}
	email := r.PostFormValue("email")
	ip := requestIP(r)
	// 登录锁定：达阈值后临时拒绝，阻断爆破。
	if a.Lockout != nil {
		if locked, lerr := a.Lockout.Locked(r.Context(), email); lerr == nil && locked {
			w.WriteHeader(http.StatusTooManyRequests)
			a.renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": a.adminCaptchaRequired(r), "CSRF": a.adminCSRF(w, r), "Error": "尝试次数过多，账户已临时锁定，请 15 分钟后再试"})
			return
		}
	}
	id, err := a.Admins.Verify(r.Context(), email, r.PostFormValue("password"))
	if err != nil {
		if a.Lockout != nil {
			_ = a.Lockout.Fail(r.Context(), email)
			_ = a.Lockout.Fail(r.Context(), adminLoginFailKey(ip)) // 失败后强制验证码
		}
		w.WriteHeader(http.StatusUnauthorized)
		a.renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": a.adminCaptchaRequired(r), "CSRF": a.adminCSRF(w, r), "Error": "用户名或密码错误"})
		return
	}
	// 两步验证（若已启用）
	if a.Admins != nil {
		if secret, enabled, gerr := a.Admins.GetTOTP(r.Context(), id); gerr == nil && enabled && secret != "" {
			if !totp.Verify(secret, r.PostFormValue("totp"), time.Now()) {
				if a.Lockout != nil {
					_ = a.Lockout.Fail(r.Context(), email)
					_ = a.Lockout.Fail(r.Context(), adminLoginFailKey(ip))
				}
				w.WriteHeader(http.StatusUnauthorized)
				a.renderAuth(w, map[string]any{"IsAdmin": true, "CaptchaScene": "admin_login", "CaptchaAdminLoginEnabled": a.adminCaptchaRequired(r), "CSRF": a.adminCSRF(w, r), "Error": "两步验证码错误"})
				return
			}
		}
	}
	if a.Lockout != nil {
		_ = a.Lockout.Clear(r.Context(), email)
		_ = a.Lockout.Clear(r.Context(), adminLoginFailKey(ip))
	}
	sess := a.AdminStore.Start(r, w)
	sess.IsAdmin = true
	sess.UserID = id
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// passwordForm GET /admin/password — 管理员修改自己密码。
func (a *Admin) passwordForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
		return
	}
	a.renderAdmin(w, "admin_password.html", AdminData{
		CSRF:  a.adminCSRF(w, r),
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
	if a.AdminStore != nil {
		a.AdminStore.RevokeAdmin(sess.UserID)
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
	a.renderAdmin(w, "admin_totp.html", AdminData{
		CSRF:          a.adminCSRF(w, r),
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
