package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/captcha"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

type Auth struct {
	Users        *repo.Users
	Sessions     *middleware.Store
	Settings     *repo.Settings
	Lockout      *repo.LoginAttempts
	Notifier     *service.Notifier
	Challenges   *service.AuthChallengeService
	Captcha      service.CaptchaProvider
	LocalCaptcha *captcha.Service
	*Deps
}

func (h *Auth) Register(mux *http.ServeMux) {
	// 登录/注册页现由前台 SPA 承载，仅保留 JSON 接口。
	mux.HandleFunc("POST /register", h.registerSubmit)
	mux.HandleFunc("POST /auth/register-code", h.registerCode)
	mux.HandleFunc("GET /captcha", h.captchaImage)
	mux.HandleFunc("GET /auth/captcha/config", h.externalCaptchaConfig)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /auth/phone-code", h.phoneCode)
	mux.HandleFunc("POST /auth/login-by-code", h.loginByPhoneCode)
	mux.HandleFunc("POST /logout", h.logout)
}

func (h *Auth) settingOn(ctx context.Context, key string, fallback bool) bool {
	if h.Settings == nil {
		return fallback
	}
	return h.Settings.Bool(ctx, key, fallback)
}

func (h *Auth) captchaImage(w http.ResponseWriter, r *http.Request) {
	if h.LocalCaptcha == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	scene := strings.TrimSpace(r.URL.Query().Get("scene"))
	force := (scene == "register" && h.settingOn(r.Context(), "captcha_register_enabled", false)) || (scene == "email_code" && h.settingOn(r.Context(), "registration_email_verification_required", false)) || (scene == "phone_code" && h.settingOn(r.Context(), "registration_phone_verification_required", true))
	// 管理员登录：该 IP 近期登录失败过则强制出验证码（与登录页/校验逻辑一致）。
	if scene == "admin_login" && h.Lockout != nil {
		if n, err := h.Lockout.Fails(r.Context(), adminLoginFailKey(requestIP(r))); err == nil && n > 0 {
			force = true
		}
	}
	var id string
	var image []byte
	var err error
	if force {
		id, image, err = h.LocalCaptcha.IssueForced(r.Context(), scene, requestIP(r))
	} else {
		id, image, err = h.LocalCaptcha.Issue(r.Context(), scene, requestIP(r))
	}
	if err != nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, map[string]any{"enabled": true, "id": id, "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)})
}

func (h *Auth) externalCaptchaConfig(w http.ResponseWriter, r *http.Request) {
	scene := strings.TrimSpace(r.URL.Query().Get("scene"))
	if scene != "register" && scene != "login" && scene != "phone_login_code" {
		http.Error(w, "验证码场景无效", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if h.Captcha == nil {
		writeJSON(w, service.CaptchaPublicConfig{Scene: scene})
		return
	}
	writeJSON(w, h.Captcha.PublicConfig(r.Context(), scene))
}

func (h *Auth) registerSettings(ctx context.Context) (bool, bool, bool, bool) {
	return h.settingOn(ctx, "registration_email_enabled", true), h.settingOn(ctx, "registration_phone_enabled", false), h.settingOn(ctx, "registration_email_verification_required", false), h.settingOn(ctx, "registration_phone_verification_required", true)
}

// jsonVals 在 SPA（Accept: application/json）时把请求体解析为字段表返回，否则返回 nil。
// SSR（浏览器导航）沿用 PostFormValue 惰性解析，不受影响。
func jsonVals(r *http.Request) map[string]string {
	if !wantsJSON(r) {
		return nil
	}
	vals, err := bodyValues(r)
	if err != nil {
		return map[string]string{}
	}
	return vals
}

// captchaCheckVals 支持 SPA JSON 请求体：vals 为空时回退 r.PostFormValue。
func (h *Auth) captchaCheckVals(ctx context.Context, scene string, r *http.Request, forceLocal bool, vals map[string]string) error {
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if scene == "client_register" {
		scene = "register"
	}
	if scene == "client_login" {
		scene = "login"
	}
	localScene := scene
	if scene == "phone_login_code" {
		localScene = "phone_code"
	}
	if forceLocal {
		if h.LocalCaptcha == nil {
			return errors.New("图形验证码服务未配置")
		}
		id, answer := fv("captcha_id"), fv("captcha_answer")
		if localScene == "email_code" || localScene == "phone_code" {
			if v := fv("captcha_id_code"); v != "" {
				id = v
			}
			if v := fv("captcha_answer_code"); v != "" {
				answer = v
			}
		}
		return h.LocalCaptcha.VerifyForced(ctx, localScene, id, answer, requestIP(r))
	}
	localEnabled := h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, localScene)
	externalRequested := h.Captcha != nil && h.Captcha.ExternalRequested(ctx, scene)
	if localEnabled && externalRequested {
		return errors.New("本地与外部验证码配置冲突")
	}
	if localEnabled {
		return h.LocalCaptcha.Verify(ctx, localScene, fv("captcha_id"), fv("captcha_answer"), requestIP(r))
	}
	if !externalRequested {
		return nil
	}
	cfg := h.Captcha.PublicConfig(ctx, scene)
	if !cfg.Enabled {
		return errors.New("外部人机验证配置不完整")
	}
	payload := map[string]string{}
	limits := map[string]int{"captcha_token": 4096, "lot_number": 256, "captcha_output": 8192, "pass_token": 8192, "gen_time": 64, "knock": 4096, "dfu": 1024, "ip": 64}
	for k, max := range limits {
		v := fv(k)
		if len(v) > max || strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("验证码参数无效")
		}
		payload[k] = v
	}
	payload["token"] = payload["captcha_token"]
	return h.Captcha.Verify(ctx, scene, payload, requestIP(r))
}

func (h *Auth) registerCode(w http.ResponseWriter, r *http.Request) {
	if h.Challenges == nil {
		jsonStatus(w, r, 503, "验证码服务未配置")
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	mode := fv("mode")
	scene := "email_code"
	force := false
	if mode == "email" {
		force = h.settingOn(r.Context(), "registration_email_verification_required", false)
	} else if mode == "phone" {
		scene = "phone_code"
		force = h.settingOn(r.Context(), "registration_phone_verification_required", true)
	} else {
		jsonStatus(w, r, 400, "注册方式无效")
		return
	}
	if err := h.captchaCheckVals(r.Context(), scene, r, force, vals); err != nil {
		jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
		return
	}
	if mode == "email" {
		email, e := repo.NormalizeEmail(fv("email"))
		if e != nil {
			jsonStatus(w, r, 400, "邮箱格式不正确")
			return
		}
		if h.Notifier == nil || !h.Notifier.EmailEnabled(r.Context()) {
			jsonStatus(w, r, 503, "邮件服务未配置")
			return
		}
		if e = h.Challenges.Issue(r.Context(), "email", "register", email, requestIP(r)); e != nil {
			jsonStatus(w, r, 502, "验证码发送失败，请稍后重试")
			return
		}
	} else {
		phone, e := service.NormalizePhone(fv("phone"))
		if e != nil {
			jsonStatus(w, r, 400, "手机号格式不正确")
			return
		}
		if e = h.Challenges.Issue(r.Context(), "phone", "register", phone, requestIP(r)); e != nil {
			jsonStatus(w, r, 502, "验证码发送失败，请稍后重试")
			return
		}
	}
	jsonStatus(w, r, 202, "验证码已发送")
}

func (h *Auth) registerSubmit(w http.ResponseWriter, r *http.Request) {
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	emailEnabled, phoneEnabled, emailVerify, phoneVerify := h.registerSettings(r.Context())
	mode := strings.ToLower(strings.TrimSpace(fv("mode")))
	if mode == "" {
		mode = "email"
	}
	if mode == "email" && !emailEnabled {
		h.renderRegisterError(w, r, "邮箱注册未启用")
		return
	}
	if mode == "phone" && !phoneEnabled {
		h.renderRegisterError(w, r, "手机号注册未启用")
		return
	}
	email, phone := strings.TrimSpace(fv("email")), strings.TrimSpace(fv("phone"))
	var err error
	if mode == "email" {
		email, err = repo.NormalizeEmail(email)
		if err != nil {
			h.renderRegisterError(w, r, "邮箱格式不正确")
			return
		}
		phone = ""
	} else {
		phone, err = service.NormalizePhone(phone)
		if err != nil {
			h.renderRegisterError(w, r, "手机号格式不正确")
			return
		}
		email = ""
	}
	pass := fv("password")
	if len(pass) < 8 || pass != fv("password_confirm") {
		h.renderRegisterError(w, r, "密码至少 8 位且两次输入必须一致")
		return
	}
	verify := (mode == "email" && emailVerify) || (mode == "phone" && phoneVerify)
	if verify {
		ch, dest := "phone", phone
		if mode == "email" {
			ch, dest = "email", email
		}
		if h.Challenges == nil || h.Challenges.Verify(r.Context(), ch, "register", dest, fv("code")) != nil {
			h.renderRegisterError(w, r, "验证码错误或已过期")
			return
		}
	} else if err := h.captchaCheckVals(r.Context(), "register", r, h.settingOn(r.Context(), "captcha_register_enabled", false), vals); err != nil {
		h.renderRegisterError(w, r, "请完成图形验证码后再注册")
		return
	}
	id, err := h.Users.CreateAccount(r.Context(), email, phone, pass, strings.TrimSpace(fv("name")), mode == "phone" && phoneVerify)
	if err != nil {
		h.renderRegisterError(w, r, "注册失败：账号可能已被占用")
		return
	}
	if err = h.Users.MarkVerified(r.Context(), id); err != nil {
		jsonStatus(w, r, 500, "注册完成失败")
		return
	}
	sess := h.Sessions.Start(r, w)
	sess.UserID = id
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (h *Auth) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string) {
	writeJSON(w, map[string]any{"ok": 0, "msg": msg})
}

func (h *Auth) phoneCode(w http.ResponseWriter, r *http.Request) {
	if !h.settingOn(r.Context(), "login_phone_otp_enabled", false) {
		jsonStatus(w, r, 403, "手机号验证码登录未启用")
		return
	}
	if h.Challenges == nil {
		jsonStatus(w, r, 503, "短信服务未配置")
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	phone, e := service.NormalizePhone(fv("phone"))
	if e != nil {
		jsonStatus(w, r, 400, "手机号格式不正确")
		return
	}
	if e = h.captchaCheckVals(r.Context(), "phone_login_code", r, false, vals); e != nil {
		jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
		return
	}
	user, _, e := h.Users.ByPhone(r.Context(), phone)
	if e != nil || user == nil || !user.PhoneVerified {
		jsonStatus(w, r, 202, "如果手机号已绑定且可用，验证码将发送到手机")
		return
	}
	if e = h.Challenges.Issue(r.Context(), "phone", "login", phone, requestIP(r)); e != nil {
		jsonStatus(w, r, 502, "验证码发送失败，请稍后重试")
		return
	}
	jsonStatus(w, r, 202, "验证码已发送")
}
func (h *Auth) loginByPhoneCode(w http.ResponseWriter, r *http.Request) {
	if !h.settingOn(r.Context(), "login_phone_otp_enabled", false) {
		jsonStatus(w, r, 403, "手机号验证码登录未启用")
		return
	}
	if h.Challenges == nil {
		jsonStatus(w, r, 503, "短信服务未配置")
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	phone, e := service.NormalizePhone(fv("phone"))
	if e != nil || h.Challenges.Verify(r.Context(), "phone", "login", phone, fv("code")) != nil {
		jsonStatus(w, r, 401, "手机号或验证码错误")
		return
	}
	user, _, e := h.Users.ByPhone(r.Context(), phone)
	if e != nil || user == nil || !user.Verified {
		jsonStatus(w, r, 401, "手机号或验证码错误")
		return
	}
	_ = h.Users.TouchLogin(r.Context(), user.ID)
	sess := h.Sessions.Start(r, w)
	sess.UserID = user.ID
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, safeNext(fv("next")), 303)
}
func (h *Auth) loginError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"ok": 0, "msg": msg})
}
func (h *Auth) loginSubmit(w http.ResponseWriter, r *http.Request) {
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if e := h.captchaCheckVals(r.Context(), "login", r, false, vals); e != nil {
		h.loginError(w, r, 401, "请完成验证码后再登录")
		return
	}
	identifier := strings.TrimSpace(fv("email"))
	lock := strings.ToLower(identifier)
	if h.Lockout != nil {
		if locked, e := h.Lockout.Locked(r.Context(), lock); e == nil && locked {
			h.loginError(w, r, 429, "尝试次数过多，请稍后再试")
			return
		}
	}
	var u *repo.User
	var hash []byte
	var e error
	if strings.Contains(identifier, "@") {
		var email string
		email, e = repo.NormalizeEmail(identifier)
		if e == nil {
			u, hash, e = h.Users.ByEmail(r.Context(), email)
			lock = email
		}
	} else {
		var phone string
		phone, e = service.NormalizePhone(identifier)
		if e == nil {
			u, hash, e = h.Users.ByPhone(r.Context(), phone)
			lock = phone
		}
	}
	if e != nil || u == nil || !h.Users.VerifyPassword(hash, fv("password")) {
		if h.Lockout != nil {
			_ = h.Lockout.Fail(r.Context(), lock)
		}
		time.Sleep(300 * time.Millisecond)
		h.loginError(w, r, 401, "邮箱、手机号或密码错误")
		return
	}
	if !u.Verified {
		h.loginError(w, r, 401, "请先完成邮箱验证")
		return
	}
	if h.Lockout != nil {
		_ = h.Lockout.Clear(r.Context(), lock)
	}
	_ = h.Users.TouchLogin(r.Context(), u.ID)
	sess := h.Sessions.Start(r, w)
	sess.UserID = u.ID
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, safeNext(fv("next")), 303)
}
func (h *Auth) logout(w http.ResponseWriter, r *http.Request) {
	h.Sessions.Destroy(r, w)
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/login", 303)
}
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") || strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}
func csrfOf(s *middleware.Store, w http.ResponseWriter, r *http.Request) string {
	if sess := middleware.FromSession(r.Context()); sess != nil {
		return sess.CSRFToken()
	}
	ns := s.Start(r, w)
	*r = *r.WithContext(middleware.WithSession(r.Context(), ns))
	return ns.CSRFToken()
}
