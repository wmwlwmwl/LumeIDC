package handler

import (
	"database/sql"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/captcha"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
	"lumeidc/internal/storage"
	"lumeidc/internal/totp"
	"lumeidc/internal/update"

	qrcode "github.com/skip2/go-qrcode"
)

type Admin struct {
	DB            *sql.DB
	PrivateFiles  *storage.PrivateFiles
	Admins        *repo.Admins
	Lockout       *repo.LoginAttempts
	Announcements *repo.Announcements
	LocalCaptcha  *captcha.Service
	Coupons       *repo.Coupons
	Refunds       *repo.Refunds
	AdminLog      *repo.AdminLog
	Stats         *repo.Stats
	Promotions    *repo.Promotions
	Notifier      *service.Notifier
	emailTestMu   sync.Mutex
	Updater       *update.Client // 系统在线更新（nil 时页面提示未启用）
	// ListenSwitcher 热切换监听端口（后台站点设置调用；nil 时仅保存不切换）。
	ListenSwitcher func(addr string) error
	// DefaultListen config.yaml 的 listen；后台端口留空时回退到该值。
	DefaultListen string
	*Deps
}

func (a *Admin) Register(mux *http.ServeMux) {
	// 后台登录页由后台 SPA 承载（/admin/login 导航返回 admin.html），仅保留 JSON 接口。
	mux.HandleFunc("POST /admin/login", a.loginSubmit)
	// 退出后台登录（与前台 /logout 分属两个会话通道）
	mux.HandleFunc("POST /admin/logout", a.logout)
	mux.HandleFunc("GET /admin", a.dashboard)
	mux.HandleFunc("GET /admin/notifications", a.adminNotifications)
	mux.HandleFunc("GET /admin/orders", a.adminOrders)
	mux.HandleFunc("GET /admin/logs", a.adminLogs)
	mux.HandleFunc("GET /admin/refunds", a.adminRefunds)
	mux.HandleFunc("GET /admin/announcements", a.adminAnnouncements)
	mux.HandleFunc("GET /admin/tickets", a.adminTickets)
	mux.HandleFunc("GET /admin/tickets/stats", a.adminTicketStats)
	mux.HandleFunc("GET /admin/tickets/{ticketID}", a.adminTicketDetail)
	mux.HandleFunc("POST /admin/tickets/{ticketID}/reply", a.adminTicketReply)
	mux.HandleFunc("POST /admin/tickets/{ticketID}/status", a.adminTicketStatus)
	mux.HandleFunc("POST /admin/tickets/{ticketID}/assign", a.adminTicketAssign)
	mux.HandleFunc("POST /admin/tickets/{ticketID}/internal-note", a.adminTicketInternalNote)
	mux.HandleFunc("POST /admin/tickets/{ticketID}/attachments", a.adminTicketAttachment)
	mux.HandleFunc("GET /admin/tickets/{ticketID}/attachments/{attachmentID}", a.adminTicketAttachmentDownload)
	mux.HandleFunc("GET /admin/ticket-assignees", a.adminTicketAssignees)
	mux.HandleFunc("GET /admin/announcements/edit", a.adminAnnouncementForm)
	mux.HandleFunc("POST /admin/announcements/save", a.adminAnnouncementSave)
	mux.HandleFunc("POST /admin/announcements/{id}/delete", a.adminAnnouncementDelete)
	mux.HandleFunc("GET /admin/coupons", a.adminCoupons)
	mux.HandleFunc("POST /admin/coupons/save", a.adminCouponCreate)
	// 营销活动
	mux.HandleFunc("GET /admin/promotions", a.adminPromotions)
	mux.HandleFunc("GET /admin/promotions/{id}", a.adminPromotionDetail)
	mux.HandleFunc("POST /admin/promotions/save", a.adminPromotionSave)
	mux.HandleFunc("POST /admin/promotions/{id}/save", a.adminPromotionSave)
	mux.HandleFunc("POST /admin/promotions/{id}/delete", a.adminPromotionDelete)
	mux.HandleFunc("POST /admin/promotions/{id}/toggle", a.adminPromotionToggle)
	mux.HandleFunc("GET /admin/promotions/{id}/stats", a.adminPromotionStats)
	mux.HandleFunc("GET /admin/settings", a.adminSettings)
	mux.HandleFunc("POST /admin/settings", a.adminSettingsSave)
	mux.HandleFunc("POST /admin/settings/test-email", a.adminTestEmail)
	mux.HandleFunc("GET /admin/email-templates", a.adminEmailTemplates)
	mux.HandleFunc("POST /admin/email-templates/master", a.adminEmailTemplateMaster)
	mux.HandleFunc("POST /admin/email-templates/save", a.adminEmailTemplateSave)
	mux.HandleFunc("POST /admin/email-templates/reset", a.adminEmailTemplateReset)
	mux.HandleFunc("POST /admin/email-templates/preview", a.adminEmailTemplatePreview)
	mux.HandleFunc("POST /admin/email-templates/test", a.adminEmailTemplateTest)
	mux.HandleFunc("GET /admin/sms-providers", a.adminSMSProviders)
	mux.HandleFunc("POST /admin/sms-templates/remote", a.adminSMSTemplateRemote)
	mux.HandleFunc("GET /admin/sms-templates", a.adminSMSTemplates)
	mux.HandleFunc("POST /admin/sms-templates/save", a.adminSMSTemplateSave)
	mux.HandleFunc("POST /admin/sms-templates/delete", a.adminSMSTemplateDelete)
	mux.HandleFunc("POST /admin/sms-templates/preview", a.adminSMSTemplatePreview)
	mux.HandleFunc("POST /admin/sms-templates/unlock", a.adminSMSTemplateUnlock)
	mux.HandleFunc("GET /admin/sms-scenes", a.adminSMSScenes)
	mux.HandleFunc("POST /admin/sms-scenes/save", a.adminSMSBindingSave)
	mux.HandleFunc("GET /admin/sms-deliveries", a.adminSMSDeliveries)
	mux.HandleFunc("GET /admin/site", a.adminSite)
	mux.HandleFunc("POST /admin/site", a.adminSiteSave)
	mux.HandleFunc("GET /admin/totp", a.totpSetup)
	mux.HandleFunc("POST /admin/totp", a.totpSetupPost)
	// 两步验证二维码（otpauth:// 的 PNG，便于验证器扫码而非手输密钥）
	mux.HandleFunc("GET /admin/totp/qr", a.totpQR)
	mux.HandleFunc("GET /admin/password", a.passwordForm)
	mux.HandleFunc("POST /admin/password", a.passwordSubmit)
	// 修改管理员登录名（需当前密码确认）
	mux.HandleFunc("POST /admin/username", a.usernameSubmit)
	mux.HandleFunc("GET /admin/update", a.adminUpdatePage)
	mux.HandleFunc("POST /admin/update/check", a.adminUpdateCheck)
	mux.HandleFunc("POST /admin/update/apply", a.adminUpdateApply)
	mux.HandleFunc("POST /admin/update/restart", a.adminUpdateRestart)
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

// captchaCheck 校验后台登录图形验证码；vals 为 JSON 请求体字段（SPA），为 nil 时回退表单。
func (a *Admin) captchaCheck(r *http.Request, vals map[string]string) error {
	if a.LocalCaptcha == nil || !a.adminCaptchaRequired(r) {
		return nil
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	// VerifyForced：失败后强制校验，不因后台开关关闭而跳过。
	return a.LocalCaptcha.VerifyForced(r.Context(), "admin_login", fv("captcha_id"), fv("captcha_answer"), requestIP(r))
}

func (a *Admin) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if a.AdminStore == nil {
		jsonStatus(w, r, 500, "服务器内部错误")
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if err := a.captchaCheck(r, vals); err != nil {
		if a.Lockout != nil {
			if lerr := a.Lockout.Fail(r.Context(), adminLoginFailKey(requestIP(r))); lerr != nil {
				log.Printf("[admin] 记录验证码失败出错: %v", lerr)
			}
		}
		jsonStatus(w, r, 401, "图形验证码错误，请重试")
		return
	}
	fail := func(status int, msg string) {
		jsonStatus(w, r, status, msg)
	}
	email := strings.TrimSpace(fv("email"))
	ip := requestIP(r)
	// 登录锁定：达阈值后临时拒绝，阻断爆破。
	if a.Lockout != nil {
		if locked, lerr := a.Lockout.Locked(r.Context(), email); lerr == nil && locked {
			fail(http.StatusTooManyRequests, "尝试次数过多，账户已临时锁定，请 15 分钟后再试")
			return
		}
	}
	id, err := a.Admins.Verify(r.Context(), email, fv("password"))
	if err != nil {
		if a.Lockout != nil {
			if lerr := a.Lockout.Fail(r.Context(), email); lerr != nil {
				log.Printf("[admin] 记录账号锁定失败出错 (%s): %v", email, lerr)
			}
			if lerr := a.Lockout.Fail(r.Context(), adminLoginFailKey(ip)); lerr != nil {
				log.Printf("[admin] 记录IP锁定失败出错 (%s): %v", ip, lerr)
			}
		}
		fail(http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	// 两步验证（若已启用）；a.Admins 在上方 Verify 已解引用，此处无需再判空
	if secret, enabled, gerr := a.Admins.GetTOTP(r.Context(), id); gerr == nil && enabled && secret != "" {
		if !totp.Verify(secret, fv("totp"), time.Now()) {
			if a.Lockout != nil {
				if lerr := a.Lockout.Fail(r.Context(), email); lerr != nil {
					log.Printf("[admin] 记录 TOTP 失败账号锁定出错 (%s): %v", email, lerr)
				}
				if lerr := a.Lockout.Fail(r.Context(), adminLoginFailKey(ip)); lerr != nil {
					log.Printf("[admin] 记录 TOTP 失败 IP 锁定出错 (%s): %v", ip, lerr)
				}
			}
			// SPA 明确告知该账户已启用两步验证，前端据此才展示验证码输入框，
			// 避免对未启用两步验证的账户也显示该字段。
			if wantsJSON(r) {
				w.WriteHeader(http.StatusUnauthorized)
				writeJSON(w, map[string]any{"ok": 0, "msg": "请输入两步验证码", "totp_required": true})
				return
			}
			fail(http.StatusUnauthorized, "两步验证码错误")
			return
		}
	}
	if a.Lockout != nil {
		_ = a.Lockout.Clear(r.Context(), email)
		_ = a.Lockout.Clear(r.Context(), adminLoginFailKey(ip))
	}
	sess := a.AdminStore.StartAdmin(r, w)
	sess.UserID = id
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// logout POST /admin/logout — 退出后台登录。只清管理员通道会话，
// 不影响同一浏览器上的普通用户登录。
func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if a.AdminStore != nil {
		a.AdminStore.DestroyAdmin(r, w)
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// passwordForm GET /admin/password — 管理员修改自己密码 / 登录名。
func (a *Admin) passwordForm(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
		return
	}
	username := ""
	if a.Admins != nil {
		if u, err := a.Admins.Username(r.Context(), sess.UserID); err == nil {
			username = u
		}
	}
	writeJSON(w, map[string]any{"ok": 1, "username": username})
}

// usernameSubmit POST /admin/username — 修改管理员登录名（需当前密码确认）。
func (a *Admin) usernameSubmit(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
		return
	}
	vals := jsonVals(r)
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
		http.Redirect(w, r, "/admin/password?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	if a.Admins == nil {
		fail("管理员服务未配置")
		return
	}
	oldName, _ := a.Admins.Username(r.Context(), sess.UserID)
	newName := strings.TrimSpace(fv("username"))
	if newName == oldName {
		fail("新用户名与当前相同")
		return
	}
	if err := a.Admins.ChangeUsername(r.Context(), sess.UserID, fv("password"), newName); err != nil {
		switch {
		case repo.IsWrongOldPassword(err):
			fail("密码错误，无法修改用户名")
		case repo.IsUsernameEmpty(err):
			fail("用户名不能为空")
		case repo.IsUsernameTaken(err):
			fail("用户名已被占用")
		default:
			fail("修改失败，请稍后重试")
		}
		return
	}
	if a.AdminLog != nil {
		a.AdminLog.Record(sess.UserID, "admin_username_changed", "admin", sess.UserID, "old="+oldName+" new="+newName, requestIP(r))
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "用户名已更新，下次登录请使用新用户名"})
		return
	}
	http.Redirect(w, r, "/admin/password?ok="+url.QueryEscape("用户名已更新"), http.StatusSeeOther)
}

// passwordSubmit POST /admin/password — 验证旧密码后更新。
func (a *Admin) passwordSubmit(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/password?err=表单解析失败", http.StatusSeeOther)
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
		http.Redirect(w, r, "/admin/password?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	err := a.Admins.ChangePassword(r.Context(), sess.UserID,
		fv("old_password"), fv("new_password"))
	if err != nil {
		msg := "修改失败"
		switch {
		case repo.IsWrongOldPassword(err):
			msg = "旧密码错误"
		case repo.IsPasswordTooShort(err):
			msg = "新密码至少8位"
		}
		fail(msg)
		return
	}
	if a.AdminStore != nil {
		a.AdminStore.RevokeAdmin(sess.UserID)
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "密码已修改，请重新登录"})
		return
	}
	http.Redirect(w, r, "/admin/login?ok="+url.QueryEscape("密码已修改，请重新登录"), http.StatusSeeOther)
}

// totpSetup GET /admin/totp — 两步验证设置页（生成/展示密钥，确认后启用）。
func (a *Admin) totpSetup(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
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
	writeJSON(w, map[string]any{"ok": 1, "secret": secret, "enabled": enabled, "uri": uri})
}

// totpSetupPost POST /admin/totp — 启用（校验码）或关闭两步验证。
// totpQR GET /admin/totp/qr — 两步验证密钥的二维码（PNG）。
// 便于用验证器扫码，避免手输 32 位密钥出错；密钥本就随 JSON 下发，无新增暴露面。
func (a *Admin) totpQR(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
		return
	}
	secret, enabled, err := a.Admins.GetTOTP(r.Context(), sess.UserID)
	if err != nil || enabled || secret == "" {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(totp.URI("LumeIDC-Admin", "LumeIDC", secret), qrcode.Medium, 220)
	if err != nil {
		http.Error(w, "生成二维码失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

func (a *Admin) totpSetupPost(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	sess := middleware.FromSession(r.Context())
	if sess == nil {
		return
	}
	vals := jsonVals(r)
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
		http.Redirect(w, r, "/admin/totp?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	success := func() {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
			return
		}
		http.Redirect(w, r, "/admin/totp?ok=1", http.StatusSeeOther)
	}
	if fv("action") == "disable" {
		_ = a.Admins.DisableTOTP(r.Context(), sess.UserID)
		success()
		return
	}
	secret, _, _ := a.Admins.GetTOTP(r.Context(), sess.UserID)
	if !totp.Verify(secret, fv("code"), time.Now()) {
		fail("验证码错误")
		return
	}
	if err := a.Admins.EnableTOTP(r.Context(), sess.UserID); err != nil {
		fail(err.Error())
		return
	}
	success()
}
