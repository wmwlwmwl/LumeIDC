package handler

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"

	"lumeidc/internal/captcha"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

//go:embed templates/auth.html
var authFS embed.FS

type Auth struct {
	Users        *repo.Users
	Sessions     *middleware.Store
	Lockout      *repo.LoginAttempts
	Notifier     *service.Notifier
	Challenges   *service.AuthChallengeService
	Captcha      service.CaptchaProvider
	LocalCaptcha *captcha.Service
	BaseURL      string
}

func (h *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /register", h.registerForm)
	mux.HandleFunc("POST /register", h.registerSubmit)
	mux.HandleFunc("POST /auth/register-code", h.registerCode)
	mux.HandleFunc("GET /captcha", h.captchaImage)
	mux.HandleFunc("GET /auth/captcha/config", h.externalCaptchaConfig)
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /auth/phone-code", h.phoneCode)
	mux.HandleFunc("POST /auth/login-by-code", h.loginByPhoneCode)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /verify", h.verifyEmail)
}

func renderAuth(w http.ResponseWriter, data map[string]any) {
	data["SiteName"] = "LumeIDC"
	tpl, err := template.ParseFS(authFS, "templates/auth.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tpl.Execute(w, data); err != nil {
		log.Printf("[template] auth.html 执行失败: %v", err)
	}
}

func (h *Auth) settingOn(ctx context.Context, key string, fallback bool) bool {
	if h.Users == nil || h.Users.DB == nil {
		return fallback
	}
	var value string
	if err := h.Users.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&value); err != nil {
		return fallback
	}
	return value == "1"
}

func (h *Auth) authFormData(ctx context.Context, w http.ResponseWriter, r *http.Request, register bool) map[string]any {
	data := map[string]any{"CSRF": csrfOf(h.Sessions, w, r)}
	if register {
		data["CaptchaScene"] = "register"
		data["EmailRegistrationEnabled"] = h.settingOn(ctx, "registration_email_enabled", true)
		data["PhoneRegistrationEnabled"] = h.settingOn(ctx, "registration_phone_enabled", false)
		data["EmailVerificationRequired"] = h.settingOn(ctx, "registration_email_verification_required", false)
		data["PhoneVerificationRequired"] = h.settingOn(ctx, "registration_phone_verification_required", true)
		data["RegistrationShowAllMethods"] = h.settingOn(ctx, "registration_show_all_methods", false)
		emailForce := h.settingOn(ctx, "registration_email_verification_required", false)
		phoneForce := h.settingOn(ctx, "registration_phone_verification_required", true)
		localRegister := h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "register")
		externalRegister := h.Captcha != nil && h.Captcha.ExternalRequested(ctx, "register")
		data["CaptchaRegisterEnabled"] = localRegister
		data["EmailDirectCaptchaEnabled"] = !emailForce && localRegister
		data["PhoneDirectCaptchaEnabled"] = !phoneForce && localRegister
		data["EmailExternalCaptcha"] = !emailForce && externalRegister
		data["PhoneExternalCaptcha"] = !phoneForce && externalRegister
		data["CaptchaEmailCodeEnabled"] = emailForce || (h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "email_code"))
		data["CaptchaPhoneCodeEnabled"] = phoneForce || (h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "phone_code"))
		data["ExternalRegisterCaptcha"] = externalRegister
	} else {
		data["CaptchaScene"] = "login"
		data["CaptchaLoginEnabled"] = h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "login")
		data["ExternalLoginCaptcha"] = h.Captcha != nil && h.Captcha.ExternalRequested(ctx, "login")
		data["ExternalPhoneLoginCaptcha"] = h.Captcha != nil && h.Captcha.ExternalRequested(ctx, "phone_login_code")
	}
	data["PhoneOTPLoginEnabled"] = h.settingOn(ctx, "login_phone_otp_enabled", false)
	data["CaptchaPhoneCodeEnabled"] = h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "phone_code")
	data["CaptchaAdminLoginEnabled"] = h.LocalCaptcha != nil && h.LocalCaptcha.Enabled(ctx, "admin_login")
	return data
}

func (h *Auth) registerForm(w http.ResponseWriter, r *http.Request) {
	data := h.authFormData(r.Context(), w, r, true)
	data["IsRegister"] = true
	renderAuth(w, data)
}
func (h *Auth) loginForm(w http.ResponseWriter, r *http.Request) {
	data := h.authFormData(r.Context(), w, r, false)
	data["Next"] = safeNext(r.URL.Query().Get("next"))
	renderAuth(w, data)
}

func (h *Auth) captchaImage(w http.ResponseWriter, r *http.Request) {
	if h.LocalCaptcha == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	scene := strings.TrimSpace(r.URL.Query().Get("scene"))
	force := (scene == "register" && h.settingOn(r.Context(), "captcha_register_enabled", false)) || (scene == "email_code" && h.settingOn(r.Context(), "registration_email_verification_required", false)) || (scene == "phone_code" && h.settingOn(r.Context(), "registration_phone_verification_required", true))
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

func (h *Auth) captchaCheck(ctx context.Context, scene string, r *http.Request, forceLocal bool) error {
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
		id, answer := r.PostFormValue("captcha_id"), r.PostFormValue("captcha_answer")
		if localScene == "email_code" || localScene == "phone_code" {
			if v := r.PostFormValue("captcha_id_code"); v != "" {
				id = v
			}
			if v := r.PostFormValue("captcha_answer_code"); v != "" {
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
		return h.LocalCaptcha.Verify(ctx, localScene, r.PostFormValue("captcha_id"), r.PostFormValue("captcha_answer"), requestIP(r))
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
		v := r.PostFormValue(k)
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
		http.Error(w, "验证码服务未配置", 503)
		return
	}
	mode := r.PostFormValue("mode")
	scene := "email_code"
	force := false
	if mode == "email" {
		force = h.settingOn(r.Context(), "registration_email_verification_required", false)
	} else if mode == "phone" {
		scene = "phone_code"
		force = h.settingOn(r.Context(), "registration_phone_verification_required", true)
	} else {
		http.Error(w, "注册方式无效", 400)
		return
	}
	if err := h.captchaCheck(r.Context(), scene, r, force); err != nil {
		http.Error(w, "请完成图形验证码后再获取验证码", 403)
		return
	}
	if mode == "email" {
		email, e := repo.NormalizeEmail(r.PostFormValue("email"))
		if e != nil {
			http.Error(w, "邮箱格式不正确", 400)
			return
		}
		if h.Notifier == nil || !h.Notifier.EmailEnabled(r.Context()) {
			http.Error(w, "邮件服务未配置", 503)
			return
		}
		if e = h.Challenges.Issue(r.Context(), "email", "register", email, requestIP(r)); e != nil {
			http.Error(w, "验证码发送失败，请稍后重试", 502)
			return
		}
	} else {
		phone, e := service.NormalizePhone(r.PostFormValue("phone"))
		if e != nil {
			http.Error(w, "手机号格式不正确", 400)
			return
		}
		if e = h.Challenges.Issue(r.Context(), "phone", "register", phone, requestIP(r)); e != nil {
			http.Error(w, "验证码发送失败，请稍后重试", 502)
			return
		}
	}
	http.Error(w, "验证码已发送", 202)
}

func (h *Auth) registerSubmit(w http.ResponseWriter, r *http.Request) {
	emailEnabled, phoneEnabled, emailVerify, phoneVerify := h.registerSettings(r.Context())
	mode := strings.ToLower(strings.TrimSpace(r.PostFormValue("mode")))
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
	email, phone := strings.TrimSpace(r.PostFormValue("email")), strings.TrimSpace(r.PostFormValue("phone"))
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
	pass := r.PostFormValue("password")
	if len(pass) < 8 || pass != r.PostFormValue("password_confirm") {
		h.renderRegisterError(w, r, "密码至少 8 位且两次输入必须一致")
		return
	}
	verify := (mode == "email" && emailVerify) || (mode == "phone" && phoneVerify)
	if verify {
		ch, dest := "phone", phone
		if mode == "email" {
			ch, dest = "email", email
		}
		if h.Challenges == nil || h.Challenges.Verify(r.Context(), ch, "register", dest, r.PostFormValue("code")) != nil {
			h.renderRegisterError(w, r, "验证码错误或已过期")
			return
		}
	} else if err := h.captchaCheck(r.Context(), "register", r, h.settingOn(r.Context(), "captcha_register_enabled", false)); err != nil {
		h.renderRegisterError(w, r, "请完成图形验证码后再注册")
		return
	}
	id, err := h.Users.CreateAccount(r.Context(), email, phone, pass, strings.TrimSpace(r.PostFormValue("name")), mode == "phone" && phoneVerify)
	if err != nil {
		h.renderRegisterError(w, r, "注册失败：账号可能已被占用")
		return
	}
	if err = h.Users.MarkVerified(r.Context(), id); err != nil {
		http.Error(w, "注册完成失败", 500)
		return
	}
	sess := h.Sessions.Start(w)
	sess.UserID = id
	http.Redirect(w, r, "/", 303)
}
func (h *Auth) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string) {
	data := h.authFormData(r.Context(), w, r, true)
	data["IsRegister"] = true
	data["Error"] = msg
	renderAuth(w, data)
}

func (h *Auth) phoneCode(w http.ResponseWriter, r *http.Request) {
	if !h.settingOn(r.Context(), "login_phone_otp_enabled", false) {
		http.Error(w, "手机号验证码登录未启用", 403)
		return
	}
	if h.Challenges == nil {
		http.Error(w, "短信服务未配置", 503)
		return
	}
	phone, e := service.NormalizePhone(r.PostFormValue("phone"))
	if e != nil {
		http.Error(w, "手机号格式不正确", 400)
		return
	}
	if e = h.captchaCheck(r.Context(), "phone_login_code", r, false); e != nil {
		http.Error(w, "请完成图形验证码后再获取验证码", 403)
		return
	}
	user, _, e := h.Users.ByPhone(r.Context(), phone)
	if e != nil || user == nil || !user.PhoneVerified {
		http.Error(w, "如果手机号已绑定且可用，验证码将发送到手机", 202)
		return
	}
	if e = h.Challenges.Issue(r.Context(), "phone", "login", phone, requestIP(r)); e != nil {
		http.Error(w, "验证码发送失败，请稍后重试", 502)
		return
	}
	http.Error(w, "验证码已发送", 202)
}
func (h *Auth) loginByPhoneCode(w http.ResponseWriter, r *http.Request) {
	if !h.settingOn(r.Context(), "login_phone_otp_enabled", false) {
		http.Error(w, "手机号验证码登录未启用", 403)
		return
	}
	if h.Challenges == nil {
		http.Error(w, "短信服务未配置", 503)
		return
	}
	phone, e := service.NormalizePhone(r.PostFormValue("phone"))
	if e != nil || h.Challenges.Verify(r.Context(), "phone", "login", phone, r.PostFormValue("code")) != nil {
		http.Error(w, "手机号或验证码错误", 401)
		return
	}
	user, _, e := h.Users.ByPhone(r.Context(), phone)
	if e != nil || user == nil || !user.Verified {
		http.Error(w, "手机号或验证码错误", 401)
		return
	}
	_ = h.Users.TouchLogin(r.Context(), user.ID)
	sess := h.Sessions.Start(w)
	sess.UserID = user.ID
	http.Redirect(w, r, safeNext(r.PostFormValue("next")), 303)
}
func (h *Auth) verifyEmail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	msg := "验证失败：链接无效或已使用"
	if r.URL.Query().Get("token") == "" {
		msg = "缺少验证参数"
	} else if h.Users.VerifyEmail(r.Context(), r.URL.Query().Get("token")) == nil {
		msg = "邮箱验证成功，请前往登录。"
	}
	fmt.Fprintf(w, verifyPageHTML, msg)
}
func (h *Auth) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if e := h.captchaCheck(r.Context(), "login", r, false); e != nil {
		w.WriteHeader(401)
		renderAuth(w, map[string]any{"CSRF": csrfOf(h.Sessions, w, r), "Error": "请完成验证码后再登录"})
		return
	}
	identifier := strings.TrimSpace(r.PostFormValue("email"))
	lock := strings.ToLower(identifier)
	if h.Lockout != nil {
		if locked, e := h.Lockout.Locked(r.Context(), lock); e == nil && locked {
			w.WriteHeader(429)
			renderAuth(w, map[string]any{"CSRF": csrfOf(h.Sessions, w, r), "Error": "尝试次数过多，请稍后再试"})
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
	if e != nil || u == nil || !h.Users.VerifyPassword(hash, r.PostFormValue("password")) {
		if h.Lockout != nil {
			_ = h.Lockout.Fail(r.Context(), lock)
		}
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(401)
		renderAuth(w, map[string]any{"CSRF": csrfOf(h.Sessions, w, r), "Error": "邮箱、手机号或密码错误"})
		return
	}
	if !u.Verified {
		w.WriteHeader(401)
		renderAuth(w, map[string]any{"CSRF": csrfOf(h.Sessions, w, r), "Error": "请先完成邮箱验证"})
		return
	}
	if h.Lockout != nil {
		_ = h.Lockout.Clear(r.Context(), lock)
	}
	_ = h.Users.TouchLogin(r.Context(), u.ID)
	sess := h.Sessions.Start(w)
	sess.UserID = u.ID
	http.Redirect(w, r, safeNext(r.PostFormValue("next")), 303)
}
func (h *Auth) logout(w http.ResponseWriter, r *http.Request) {
	h.Sessions.Destroy(r, w)
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
	ns := s.Start(w)
	*r = *r.WithContext(middleware.WithSession(r.Context(), ns))
	return ns.CSRFToken()
}
func genToken() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

const verifyPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>邮箱验证</title></head><body><h1>LumeIDC</h1><p>%s</p><p><a href="/login">前往登录</a></p></body></html>`
