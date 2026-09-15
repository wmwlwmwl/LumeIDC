package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
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
	mux.HandleFunc("POST /auth/forgot-code", h.forgotCode)
	mux.HandleFunc("POST /auth/forgot-reset", h.forgotReset)
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
	switch scene {
	case "register", "login", "phone_login_code", "register_code", "forgot_code", "profile_code":
	default:
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

// checkCaptcha 支持 SPA JSON 请求体：vals 为空时回退 r.PostFormValue。
// 本地图形码与外部验证共用；外部验证启用时优先（避免「本地+外部」双重人机验证）。
// 场景名即人机验证设置页的行（register/register_code/forgot_code/profile_code/login/phone_login_code/admin_login）。
func checkCaptcha(ctx context.Context, local *captcha.Service, provider service.CaptchaProvider, scene string, r *http.Request, forceLocal bool, vals map[string]string) error {
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
	id, answer := captchaFields(fv)
	if forceLocal {
		// 外部行为验证启用时以外部验证替代本地强制（注册需验证码场景同样外部优先）
		if provider != nil && provider.ExternalRequested(ctx, scene) {
			return verifyExternal(ctx, provider, scene, fv, requestIP(r))
		}
		if local == nil {
			return errors.New("图形验证码服务未配置")
		}
		return local.VerifyForced(ctx, scene, id, answer, requestIP(r))
	}
	localEnabled := local != nil && local.Enabled(ctx, scene)
	externalRequested := provider != nil && provider.ExternalRequested(ctx, scene)
	if externalRequested {
		return verifyExternal(ctx, provider, scene, fv, requestIP(r))
	}
	if localEnabled {
		return local.Verify(ctx, scene, id, answer, requestIP(r))
	}
	return nil
}

// captchaFields 取图形验证码载荷：本地发码场景用 captcha_id_code/captcha_answer_code 区分
// （与提交表单的 captcha_id/captcha_answer 并存），此处统一回退。
func captchaFields(fv func(string) string) (id, answer string) {
	id, answer = fv("captcha_id"), fv("captcha_answer")
	if v := fv("captcha_id_code"); v != "" {
		id = v
	}
	if v := fv("captcha_answer_code"); v != "" {
		answer = v
	}
	return id, answer
}

// verifyExternal 校验外部人机验证（载荷字段与后端 provider 约定一致）。
func verifyExternal(ctx context.Context, provider service.CaptchaProvider, scene string, fv func(string) string, ip string) error {
	cfg := provider.PublicConfig(ctx, scene)
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
	return provider.Verify(ctx, scene, payload, ip)
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
	if mode != "email" && mode != "phone" {
		jsonStatus(w, r, 400, "注册方式无效")
		return
	}
	// 注册发码前的人机验证由「注册发码」开关独立控制（captcha_register_code_enabled），
	// 与「注册提交」场景（scene=register）互不干扰；外部注册启用时发码沿用同一外部验证。
	if h.Captcha != nil && h.Captcha.ExternalRequested(r.Context(), "register") {
		if err := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "register", r, false, vals); err != nil {
			jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
			return
		}
	} else {
		regCode := ""
		if h.Settings != nil {
			v, _ := h.Settings.Get(r.Context(), "captcha_register_code_enabled")
			regCode = v
		}
		switch regCode {
		case "0":
			// 显式关闭：发码前不要求人机验证
		case "1":
			if err := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "register_code", r, false, vals); err != nil {
				jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
				return
			}
		default:
			// 存量部署未配置该键：沿用旧行为（注册需验证码强制 + 发码场景开关）
			required := (mode == "email" && h.settingOn(r.Context(), "registration_email_verification_required", false)) ||
				(mode == "phone" && h.settingOn(r.Context(), "registration_phone_verification_required", true))
			scene := "email_code"
			if mode == "phone" {
				scene = "phone_code"
			}
			if err := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, scene, r, required, vals); err != nil {
				jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
				return
			}
		}
	}
	if mode == "email" {
		email, e := repo.NormalizeEmail(fv("email"))
		if e != nil {
			jsonStatus(w, r, 400, "邮箱格式不正确")
			return
		}
		// 已注册账号不再发送注册验证码（防止骚扰/枚举，直接提示登录）
		if _, _, findErr := h.Users.ByEmail(r.Context(), email); findErr == nil {
			jsonStatus(w, r, 400, "该邮箱已注册，请直接登录")
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
		// 已注册账号不再发送注册验证码
		if _, _, findErr := h.Users.ByPhone(r.Context(), phone); findErr == nil {
			jsonStatus(w, r, 400, "该手机号已注册，请直接登录")
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
	if mode == "both" && (!emailEnabled || !phoneEnabled) {
		h.renderRegisterError(w, r, "邮箱与手机号注册未同时启用")
		return
	}
	email, phone := strings.TrimSpace(fv("email")), strings.TrimSpace(fv("phone"))
	var err error
	switch mode {
	case "email":
		email, err = repo.NormalizeEmail(email)
		if err != nil {
			h.renderRegisterError(w, r, "邮箱格式不正确")
			return
		}
		phone = ""
	case "both":
		// 邮箱 + 手机号同时注册：两个都必填（需两个允许开关均开启）
		email, err = repo.NormalizeEmail(email)
		if err != nil {
			h.renderRegisterError(w, r, "邮箱格式不正确")
			return
		}
		phone, err = service.NormalizePhone(phone)
		if err != nil {
			h.renderRegisterError(w, r, "手机号格式不正确")
			return
		}
	default:
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
	if mode == "both" {
		// 同时注册：邮箱/手机验证码按各自开关要求，任一需要即校验；都不需要则走图形验证码
		needEmail := emailVerify && h.Challenges != nil
		needPhone := phoneVerify && h.Challenges != nil
		if needEmail && h.Challenges.Verify(r.Context(), "email", "register", email, fv("email_code")) != nil {
			h.renderRegisterError(w, r, "验证码错误或已过期")
			return
		}
		if needPhone && h.Challenges.Verify(r.Context(), "phone", "register", phone, fv("phone_code")) != nil {
			h.renderRegisterError(w, r, "验证码错误或已过期")
			return
		}
		if !needEmail && !needPhone {
			if err := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "register", r, h.settingOn(r.Context(), "captcha_register_enabled", false), vals); err != nil {
				h.renderRegisterError(w, r, "请完成图形验证码后再注册")
				return
			}
		}
	} else if verify := (mode == "email" && emailVerify) || (mode == "phone" && phoneVerify); verify {
		ch, dest := "phone", phone
		if mode == "email" {
			ch, dest = "email", email
		}
		if h.Challenges == nil || h.Challenges.Verify(r.Context(), ch, "register", dest, fv("code")) != nil {
			h.renderRegisterError(w, r, "验证码错误或已过期")
			return
		}
	} else if err := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "register", r, h.settingOn(r.Context(), "captcha_register_enabled", false), vals); err != nil {
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
	if e = checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "phone_login_code", r, false, vals); e != nil {
		jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
		return
	}
	// 可否发送短信是站级全局状态，不泄露手机号是否注册，须在防枚举短路之前判定：
	// 短信未配置时给出真实失败（与注册/改绑一致），否则下面会把「未绑定手机号」伪装成发送成功。
	if !service.SMSServiceConfigured(r.Context(), h.Settings) {
		jsonStatus(w, r, 503, "短信服务未配置")
		return
	}
	// 是否注册是防枚举的最后一道底线：未注册仍返回模糊提示，不泄露注册状态。
	// 已注册（无论手机号是否已验证）都发码，验证由短信登录成功的实时验证码证明。
	user, _, e := h.Users.ByPhone(r.Context(), phone)
	if e != nil || user == nil {
		jsonStatus(w, r, 202, "如果手机号已注册且可接收短信，验证码将发送到手机")
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
	if e != nil || user == nil {
		jsonStatus(w, r, 401, "手机号或验证码错误")
		return
	}
	// 短信验证码通过 = 本人在号（实时所有权证明），未验证时借登录置为已验证（单向提升，不降级）。
	if !user.PhoneVerified {
		if err := h.Users.MarkPhoneVerified(r.Context(), user.ID); err != nil {
			log.Printf("[auth] 短信登录后标记手机号已验证失败 user=%d: %v", user.ID, err)
		}
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

// forgotChannel 按账号识别找回渠道：含 @ 走邮箱，否则按手机号（与登录判渠道一致）。
func (h *Auth) forgotChannel(account string) (channel, dest, scene string, err error) {
	if strings.Contains(account, "@") {
		dest, e := repo.NormalizeEmail(account)
		if e != nil {
			return "", "", "email_code", e
		}
		return "email", dest, "email_code", nil
	}
	dest, e := service.NormalizePhone(account)
	if e != nil {
		return "", "", "phone_code", e
	}
	return "phone", dest, "phone_code", nil
}

// forgotAccount 按渠道解析账号并限定「可找回」的用户（禁用、手机未绑定不可找回），
// 返回用户与解析错误；未命中与禁用统一按不存在处理，不向调用方区分。
func (h *Auth) forgotAccount(ctx context.Context, channel, dest string) (*repo.User, error) {
	if channel == "phone" {
		user, _, e := h.Users.ByPhone(ctx, dest)
		if e != nil || user == nil || !user.PhoneVerified {
			return nil, repo.ErrNotFound
		}
		return user, nil
	}
	user, _, e := h.Users.ByEmail(ctx, dest)
	if e != nil || user == nil {
		return nil, repo.ErrNotFound
	}
	return user, nil
}

// forgotCode POST /auth/forgot-code — 发送找回密码验证码（邮箱/手机，按账号识别渠道）。
// 防账号枚举：账号不存在/不可找回时同样返回成功文案，且不触发真实发送。
func (h *Auth) forgotCode(w http.ResponseWriter, r *http.Request) {
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
	channel, dest, _, e := h.forgotChannel(strings.TrimSpace(fv("account")))
	if e != nil {
		jsonStatus(w, r, 400, "请输入正确的邮箱或手机号")
		return
	}
	// 找回密码发码的人机验证统一走 forgot_code 场景（与渠道无关，由设置页「找回密码」行控制）。
	if e := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "forgot_code", r, false, vals); e != nil {
		jsonStatus(w, r, 403, "请完成图形验证码后再获取验证码")
		return
	}
	if user, e := h.forgotAccount(r.Context(), channel, dest); e == nil && user != nil {
		if e := h.Challenges.Issue(r.Context(), channel, "reset_password", dest, requestIP(r)); e != nil {
			jsonStatus(w, r, 502, "验证码发送失败，请稍后重试")
			return
		}
	}
	jsonStatus(w, r, 202, "如果账号存在且可用，验证码将发送到您的邮箱或手机号")
}

// forgotReset POST /auth/forgot-reset — 校验找回密码验证码并重置密码，吊销旧会话。
func (h *Auth) forgotReset(w http.ResponseWriter, r *http.Request) {
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
	channel, dest, _, e := h.forgotChannel(strings.TrimSpace(fv("account")))
	if e != nil {
		jsonStatus(w, r, 400, "请输入正确的邮箱或手机号")
		return
	}
	code := strings.TrimSpace(fv("code"))
	if len(code) == 0 || len(code) > 8 || h.Challenges.Verify(r.Context(), channel, "reset_password", dest, code) != nil {
		jsonStatus(w, r, 401, "验证码错误或已过期")
		return
	}
	user, e := h.forgotAccount(r.Context(), channel, dest)
	if e != nil || user == nil {
		jsonStatus(w, r, 401, "验证码错误或已过期")
		return
	}
	newPass, confirm := fv("password"), fv("password_confirm")
	if len(newPass) < 8 || newPass != confirm {
		jsonStatus(w, r, 400, "密码至少 8 位且两次输入必须一致")
		return
	}
	if e := h.Users.SetPassword(r.Context(), user.ID, newPass); e != nil {
		jsonStatus(w, r, 500, "密码重置失败，请稍后重试")
		return
	}
	if h.Sessions != nil {
		h.Sessions.RevokeUser(user.ID)
	}
	jsonStatus(w, r, 200, "密码已重置，请重新登录")
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
	if e := checkCaptcha(r.Context(), h.LocalCaptcha, h.Captcha, "login", r, false, vals); e != nil {
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
