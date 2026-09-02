package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

func (a *Admin) adminSettings(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	s := a.Settings
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
		"notify_email_forward_enabled":              get("notify_email_forward_enabled"),
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
		"verification_smapi_app_key":                get("verification_smapi_app_key"),
		"verification_stay33_api_url":               get("verification_stay33_api_url"),
		"verification_stay33_api_key":               get("verification_stay33_api_key"),
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
	// 多 SMTP 账号列表（回传页面时剔除密码）
	acctRaw := get(mailAccountsKey)
	var acctList []service.MailAccount
	if strings.TrimSpace(acctRaw) != "" {
		_ = json.Unmarshal([]byte(acctRaw), &acctList)
	}
	if len(acctList) == 0 && strings.TrimSpace(get("smtp_host")) != "" {
		port := 587
		if p, e := strconv.Atoi(get("smtp_port")); e == nil && p > 0 {
			port = p
		}
		acctList = []service.MailAccount{{
			Name: "默认账号", Host: get("smtp_host"), Port: port,
			User: get("smtp_user"), Pass: get("smtp_pass"), From: get("smtp_from"), Enabled: true,
		}}
	}
	if acctList == nil {
		acctList = []service.MailAccount{}
	}
	for i := range acctList {
		acctList[i].Pass = ""
	}
	dispJSON, _ := json.Marshal(acctList)
	cfg[mailAccountsKey] = string(dispJSON)
	cfg[mailCooldownKey] = get(mailCooldownKey)
	if cfg[mailCooldownKey] == "" {
		cfg[mailCooldownKey] = "60"
	}
	a.renderAdmin(w, "admin_settings.html", AdminData{
		CSRF:        a.adminCSRF(w, r),
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
	s := a.Settings
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

	if section == "automatic_identity" {
		provider := strings.ToLower(strings.TrimSpace(r.PostFormValue("verification_provider")))
		if provider != "" && provider != "baidu_face" && provider != "leaf_face" && provider != "smapi" && provider != "stay33" {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape("自动实名 provider 无效"), http.StatusSeeOther)
			return
		}
		set("verification_provider", provider)
		saveSecret := func(key string) {
			if value := strings.TrimSpace(r.PostFormValue(key)); value != "" {
				set(key, value)
			}
		}
		switch provider {
		case "baidu_face":
			set("verification_baidu_api_key", strings.TrimSpace(r.PostFormValue("verification_baidu_api_key")))
			set("verification_baidu_plan_id", strings.TrimSpace(r.PostFormValue("verification_baidu_plan_id")))
			saveSecret("verification_baidu_secret_key")
		case "leaf_face":
			set("verification_leaf_app_id", strings.TrimSpace(r.PostFormValue("verification_leaf_app_id")))
			set("verification_leaf_api_base", strings.TrimSpace(r.PostFormValue("verification_leaf_api_base")))
			saveSecret("verification_leaf_app_secret")
		case "smapi":
			set("verification_smapi_app_key", strings.TrimSpace(r.PostFormValue("verification_smapi_app_key")))
			set("verification_smapi_api_url", strings.TrimSpace(r.PostFormValue("verification_smapi_api_url")))
			set("verification_smapi_product_code", strings.TrimSpace(r.PostFormValue("verification_smapi_product_code")))
			saveSecret("verification_smapi_secret_key")
		case "stay33":
			set("verification_stay33_api_key", strings.TrimSpace(r.PostFormValue("verification_stay33_api_key")))
			set("verification_stay33_api_url", strings.TrimSpace(r.PostFormValue("verification_stay33_api_url")))
			set("verification_stay33_biz_code", strings.TrimSpace(r.PostFormValue("verification_stay33_biz_code")))
			saveSecret("verification_stay33_secret_key")
		}
		http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
		return
	}

	// SMTP/邮件服务（含旧版未带 settings_section 的综合表单）。
	accountsJSON := strings.TrimSpace(r.PostFormValue(mailAccountsKey))
	var accounts []service.MailAccount
	switch {
	case accountsJSON != "":
		if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape("邮件账号列表格式无效，请重试"), http.StatusSeeOther)
			return
		}
	case strings.TrimSpace(r.PostFormValue("smtp_host")) != "":
		// 旧版单组字段表单提交 → 转成单个账号
		port := 587
		if p, e := strconv.Atoi(strings.TrimSpace(r.PostFormValue("smtp_port"))); e == nil && p > 0 {
			port = p
		}
		accounts = []service.MailAccount{{
			Name: "默认账号", Host: strings.TrimSpace(r.PostFormValue("smtp_host")), Port: port,
			User: strings.TrimSpace(r.PostFormValue("smtp_user")), Pass: r.PostFormValue("smtp_pass"),
			From: strings.TrimSpace(r.PostFormValue("smtp_from")), Enabled: true,
		}}
	}
	// 密码留空 = 保留旧值（按 主机+用户名 匹配；首次从旧单组键迁移时回退读取）
	readKey := func(k string) string {
		v, _ := s.Get(r.Context(), k)
		return v
	}
	var oldAccounts []service.MailAccount
	if oldRaw, e := s.Get(r.Context(), mailAccountsKey); e == nil && strings.TrimSpace(oldRaw) != "" {
		_ = json.Unmarshal([]byte(oldRaw), &oldAccounts)
	}
	keepPass := func(host, user, typed string) string {
		if strings.TrimSpace(typed) != "" {
			return typed
		}
		for _, o := range oldAccounts {
			if strings.EqualFold(strings.TrimSpace(o.Host), strings.TrimSpace(host)) &&
				strings.TrimSpace(o.User) == strings.TrimSpace(user) && strings.TrimSpace(o.Pass) != "" {
				return o.Pass
			}
		}
		if legacyPass := readKey("smtp_pass"); legacyPass != "" &&
			strings.EqualFold(strings.TrimSpace(host), strings.TrimSpace(readKey("smtp_host"))) &&
			strings.TrimSpace(user) == strings.TrimSpace(readKey("smtp_user")) {
			return legacyPass
		}
		return ""
	}
	clean := make([]service.MailAccount, 0, len(accounts))
	for i, a := range accounts {
		a.Name = strings.TrimSpace(a.Name)
		a.Host = strings.TrimSpace(a.Host)
		a.User = strings.TrimSpace(a.User)
		a.From = strings.TrimSpace(a.From)
		if a.Port == 0 {
			a.Port = 587
		}
		if a.Host == "" {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape(fmt.Sprintf("第 %d 个账号未填写 SMTP 主机", i+1)), http.StatusSeeOther)
			return
		}
		if a.Port < 1 || a.Port > 65535 {
			http.Redirect(w, r, "/admin/settings?err="+url.QueryEscape(fmt.Sprintf("第 %d 个账号端口无效", i+1)), http.StatusSeeOther)
			return
		}
		a.Pass = keepPass(a.Host, a.User, a.Pass)
		clean = append(clean, a)
	}
	storedJSON, _ := json.Marshal(clean)
	if err := s.Set(r.Context(), mailAccountsKey, string(storedJSON)); err != nil {
		http.Error(w, "保存邮件账号失败", 500)
		return
	}
	// 失败冷却秒数（默认 60，可配 1~86400）
	cooldown := 60
	if v := strings.TrimSpace(r.PostFormValue(mailCooldownKey)); v != "" {
		if p, e := strconv.Atoi(v); e == nil && p >= 1 {
			cooldown = p
			if cooldown > 86400 {
				cooldown = 86400
			}
		}
	}
	if err := s.Set(r.Context(), mailCooldownKey, strconv.Itoa(cooldown)); err != nil {
		http.Error(w, "保存发送配置失败", 500)
		return
	}
	// 镜像首个账号到旧单组键（兼容旧读取路径，列表保存后仍可用）
	if len(clean) > 0 {
		first := clean[0]
		set("smtp_host", first.Host)
		set("smtp_port", strconv.Itoa(first.Port))
		set("smtp_user", first.User)
		set("smtp_pass", first.Pass)
		set("smtp_from", first.From)
	} else {
		set("smtp_host", "")
		set("smtp_port", "")
		set("smtp_user", "")
		set("smtp_pass", "")
		set("smtp_from", "")
	}
	setFlag("notify_email_forward_enabled")
	http.Redirect(w, r, "/admin/settings?ok=1", http.StatusSeeOther)
}

// adminCouponCreate POST /admin/coupons/save — 新建优惠码。

func (a *Admin) adminTestEmail(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "msg": "请求解析失败"})
		return
	}
	to := strings.TrimSpace(r.PostFormValue("email"))
	if to == "" || !strings.Contains(to, "@") {
		writeJSON(w, map[string]any{"ok": false, "msg": "请输入有效的收件邮箱"})
		return
	}
	n := a.Notifier
	siteName := n.SiteName(r.Context())
	if siteName == "" {
		siteName = service.DefaultSiteName
	}
	subject := siteName + " 邮件发送测试"
	body := "这是一封来自 " + siteName + " 的测试邮件，收到即表示 SMTP 配置生效。\r\n发送时间：" + time.Now().Format("2006-01-02 15:04:05")
	idx := -1
	if v := strings.TrimSpace(r.PostFormValue("account_index")); v != "" {
		if p, e := strconv.Atoi(v); e == nil && p >= 0 {
			idx = p
		} else {
			writeJSON(w, map[string]any{"ok": false, "msg": "账号序号无效"})
			return
		}
	}
	var err error
	if idx >= 0 {
		err = n.SendTestMailAccount(r.Context(), idx, to, subject, body)
	} else {
		err = n.SendTestMail(r.Context(), to, subject, body)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "msg": "发送失败：" + err.Error()})
		return
	}
	if idx >= 0 {
		writeJSON(w, map[string]any{"ok": true, "msg": fmt.Sprintf("测试邮件已通过账号 #%d 发送至 %s", idx+1, to)})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "msg": "测试邮件已发送至 " + to})
}
