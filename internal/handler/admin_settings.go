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
	// 一次取回全部键：原实现逐键 Get，设置页每次打开 60+ 条 SQL。
	// GetMany 只返回库中存在的键，缺失键取空串——与原 Get 失败回退行为一致。
	vals, err := s.GetMany(r.Context(),
		"manual_identity_requires_verified_phone",
		"smtp_host", "smtp_port", "smtp_user", "smtp_from",
		"sms_region", "sms_global_access_key", "sms_global_sign_name", "sms_routes",
		"sms_provider", "sms_endpoint", "sms_access_key", "sms_username",
		"sms_sign_name", "sms_template_code", "sms_template_content",
		"captcha_provider", "captcha_geetest_id", "captcha_vaptcha_vid", "captcha_corptcha_site_key",
		"verification_provider", "verification_endpoint",
		"verification_baidu_api_key", "verification_baidu_plan_id",
		"verification_leaf_app_id", "verification_leaf_api_base",
		"verification_smapi_api_url", "verification_smapi_product_code", "verification_smapi_app_key",
		"verification_stay33_api_url", "verification_stay33_api_key", "verification_stay33_biz_code",
		"manual_identity_enabled",
		"registration_email_enabled", "registration_phone_enabled",
		"registration_email_verification_required", "registration_phone_verification_required",
		// require_both 为「邮箱+手机号同时注册」开关；show_all_methods 为旧键名（兼容既有部署已开启的值）
		"registration_require_both", "registration_show_all_methods",
		"login_email_enabled", "login_phone_enabled",
		"login_phone_otp_enabled",
		// 换绑校验开关：修改邮箱/手机时是否强制验证原渠道
		"profile_change_require_old_email", "profile_change_require_old_phone",
		"captcha_enabled", "captcha_register_enabled", "captcha_login_enabled", "captcha_admin_login_enabled",
		"captcha_register_code_enabled", "captcha_forgot_code_enabled", "captcha_profile_code_enabled",
		"captcha_phone_login_code_enabled",
		"external_captcha_register_enabled", "external_captcha_login_enabled",
		"external_captcha_register_code_enabled", "external_captcha_forgot_code_enabled",
		"external_captcha_profile_code_enabled",
		"external_captcha_phone_login_code_enabled",
		// 服务生命周期天数：到期停机 / 删除 / 到期提醒
		"service_suspend_after_days", "service_terminate_after_days", "service_expire_warn_days",
		mailAccountsKey, mailCooldownKey,
	)
	if err != nil {
		log.Printf("[settings] 读取设置失败: %v", err)
	}
	get := func(k string) string {
		return vals[k]
	}
	manualRequiresPhone := get("manual_identity_requires_verified_phone")
	if manualRequiresPhone == "" {
		manualRequiresPhone = "1"
	}
	cfg := map[string]string{
		"smtp_host":                                get("smtp_host"),
		"smtp_port":                                get("smtp_port"),
		"smtp_user":                                get("smtp_user"),
		"smtp_pass":                                "", // 不回传明文；多账号列表内的密码同样已掩码
		"smtp_from":                                get("smtp_from"),
		"sms_region":                               get("sms_region"),
		"sms_global_access_key":                    get("sms_global_access_key"),
		"sms_global_sign_name":                     get("sms_global_sign_name"),
		"sms_global_secret_key":                    "",
		"sms_secret_key":                           "",
		"sms_provider":                             get("sms_provider"),
		"sms_endpoint":                             get("sms_endpoint"),
		"sms_access_key":                           get("sms_access_key"),
		"sms_username":                             get("sms_username"),
		"sms_api_key":                              "",
		"sms_sign_name":                            get("sms_sign_name"),
		"sms_template_code":                        get("sms_template_code"),
		"sms_template_content":                     get("sms_template_content"),
		"captcha_provider":                         get("captcha_provider"),
		"captcha_geetest_id":                       get("captcha_geetest_id"),
		"captcha_vaptcha_vid":                      get("captcha_vaptcha_vid"),
		"captcha_corptcha_site_key":                get("captcha_corptcha_site_key"),
		"verification_provider":                    get("verification_provider"),
		"verification_endpoint":                    get("verification_endpoint"),
		"verification_token":                       "",
		"verification_baidu_api_key":               get("verification_baidu_api_key"),
		"verification_baidu_plan_id":               get("verification_baidu_plan_id"),
		"verification_leaf_app_id":                 get("verification_leaf_app_id"),
		"verification_leaf_api_base":               get("verification_leaf_api_base"),
		"verification_smapi_api_url":               get("verification_smapi_api_url"),
		"verification_smapi_product_code":          get("verification_smapi_product_code"),
		"verification_smapi_app_key":               get("verification_smapi_app_key"),
		"verification_stay33_api_url":              get("verification_stay33_api_url"),
		"verification_stay33_api_key":              get("verification_stay33_api_key"),
		"verification_stay33_biz_code":             get("verification_stay33_biz_code"),
		"manual_identity_requires_verified_phone":  manualRequiresPhone,
		"manual_identity_enabled":                  get("manual_identity_enabled"),
		"registration_email_enabled":               get("registration_email_enabled"),
		"registration_phone_enabled":               get("registration_phone_enabled"),
		"registration_email_verification_required": get("registration_email_verification_required"),
		"registration_phone_verification_required": get("registration_phone_verification_required"),
		// 新键为空时兼容旧键名（registration_show_all_methods）
		"registration_require_both":                 fallbackOr("registration_require_both", "registration_show_all_methods", vals),
		"login_email_enabled":                       get("login_email_enabled"),
		"login_phone_enabled":                       get("login_phone_enabled"),
		"login_phone_otp_enabled":                   get("login_phone_otp_enabled"),
		"profile_change_require_old_email":          defaultOne(get("profile_change_require_old_email")),
		"profile_change_require_old_phone":          defaultOne(get("profile_change_require_old_phone")),
		"captcha_enabled":                           get("captcha_enabled"),
		"captcha_register_enabled":                  get("captcha_register_enabled"),
		"captcha_login_enabled":                     get("captcha_login_enabled"),
		"captcha_admin_login_enabled":               get("captcha_admin_login_enabled"),
		"captcha_register_code_enabled":             get("captcha_register_code_enabled"),
		"captcha_forgot_code_enabled":               get("captcha_forgot_code_enabled"),
		"captcha_profile_code_enabled":              get("captcha_profile_code_enabled"),
		"captcha_phone_login_code_enabled":          get("captcha_phone_login_code_enabled"),
		"external_captcha_register_enabled":         get("external_captcha_register_enabled"),
		"external_captcha_login_enabled":            get("external_captcha_login_enabled"),
		"external_captcha_register_code_enabled":    get("external_captcha_register_code_enabled"),
		"external_captcha_forgot_code_enabled":      get("external_captcha_forgot_code_enabled"),
		"external_captcha_profile_code_enabled":     get("external_captcha_profile_code_enabled"),
		"external_captcha_phone_login_code_enabled": get("external_captcha_phone_login_code_enabled"),
		// 生命周期天数：库中无记录时回退默认值，前端首次打开不至于显示空白
		"service_suspend_after_days":   fallbackStr(get("service_suspend_after_days"), "0"),
		"service_terminate_after_days": fallbackStr(get("service_terminate_after_days"), "3"),
		"service_expire_warn_days":     fallbackStr(get("service_expire_warn_days"), "3"),
	}
	if a.Notifier != nil {
		if routes, routeErr := a.Notifier.PublicSMSRoutes(r.Context()); routeErr == nil {
			cfg["sms_routes"] = routes
		} else {
			cfg["sms_routes"] = "{}"
		}
	}
	// 多 SMTP 账号列表（回传页面时剔除密码）
	acctRaw := get(mailAccountsKey)
	var acctList []service.MailAccount
	if strings.TrimSpace(acctRaw) != "" {
		if err := json.Unmarshal([]byte(acctRaw), &acctList); err != nil {
			log.Printf("[settings] 邮件账号列表解析失败（回退空列表展示）: %v", err)
		}
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
	// 结构为字符串键值；布尔类开关以 "1"/"0" 表示，由前端按已知键解释。
	writeJSON(w, map[string]any{"ok": 1, "cfg": cfg})
}

// adminSettingsSave POST /admin/settings — 保存 SMTP 配置。

// settingsSaveCtx 设置保存请求的共享上下文：各设置分组的私有保存方法复用
// 字段读取（fv）、写库（set/setFlag）与响应（fail/success），错误聚合到 setErr。
type settingsSaveCtx struct {
	a      *Admin
	w      http.ResponseWriter
	r      *http.Request
	vals   map[string]string
	setErr error
}

func (c *settingsSaveCtx) fv(k string) string {
	if c.vals != nil {
		return c.vals[k]
	}
	return c.r.PostFormValue(k)
}

func (c *settingsSaveCtx) set(k, v string) {
	if err := c.a.Settings.Set(c.r.Context(), k, v); err != nil {
		log.Printf("[settings] 保存 %s 失败: %v", k, err)
		if c.setErr == nil {
			c.setErr = err
		}
	}
}

func (c *settingsSaveCtx) setFlag(key string) {
	if c.fv(key) == "1" {
		c.set(key, "1")
	} else {
		c.set(key, "0")
	}
}

func (c *settingsSaveCtx) fail(msg string) {
	if wantsJSON(c.r) {
		writeJSON(c.w, map[string]any{"ok": 0, "msg": msg})
		return
	}
	http.Redirect(c.w, c.r, "/admin/settings?err="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (c *settingsSaveCtx) success() {
	// ponytail: 部分键写入失败时不能报"已保存"——DB 异常下管理员会误以为配置已生效
	if c.setErr != nil {
		c.fail("部分配置保存失败，请重试")
		return
	}
	if wantsJSON(c.r) {
		writeJSON(c.w, map[string]any{"ok": 1, "msg": "已保存"})
		return
	}
	http.Redirect(c.w, c.r, "/admin/settings?ok=1", http.StatusSeeOther)
}

func (c *settingsSaveCtx) saveRegistration() {
	for _, key := range []string{"registration_email_enabled", "registration_phone_enabled", "registration_email_verification_required", "registration_phone_verification_required", "registration_require_both", "login_phone_otp_enabled", "profile_change_require_old_email", "profile_change_require_old_phone"} {
		c.setFlag(key)
	}
}

// fallbackOr 返回 vals[newKey]，为空时回退 vals[oldKey]（设置键改名后的存量数据兼容）。
func fallbackOr(newKey, oldKey string, vals map[string]string) string {
	if v := vals[newKey]; v != "" {
		return v
	}
	return vals[oldKey]
}

// defaultOne 布尔开关的默认值兜底：未配置/缺键时按开启处理（默认安全）。
func defaultOne(v string) string {
	if v == "" {
		return "1"
	}
	return v
}

// fallbackStr 空值回退：设置键从未保存过时库中无记录，需给出默认值供前端展示。
func fallbackStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// dayInRange 读取一个天数值：空值取 fallback（未配置即默认），非数字或越界报错。
func (c *settingsSaveCtx) dayInRange(key string, min, max, fallback int) (int, bool) {
	raw := strings.TrimSpace(c.fv(key))
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		c.fail(fmt.Sprintf("%s 必须是 %d~%d 之间的整数", key, min, max))
		return 0, false
	}
	return n, true
}

// saveLifecycle 校验并保存服务生命周期天数；返回 false 表示校验失败且已写响应。
// 删除天数不得早于停机天数——否则服务会在还没停机时就被标记删除。
func (c *settingsSaveCtx) saveLifecycle() bool {
	suspend, ok1 := c.dayInRange("service_suspend_after_days", 0, 365, 0)
	terminate, ok2 := c.dayInRange("service_terminate_after_days", 1, 3650, 3)
	warn, ok3 := c.dayInRange("service_expire_warn_days", 1, 365, 3)
	if !ok1 || !ok2 || !ok3 {
		return false
	}
	if suspend > terminate {
		c.fail("删除天数不能小于停机天数")
		return false
	}
	c.set("service_suspend_after_days", strconv.Itoa(suspend))
	c.set("service_terminate_after_days", strconv.Itoa(terminate))
	c.set("service_expire_warn_days", strconv.Itoa(warn))
	return true
}

func (c *settingsSaveCtx) saveCaptcha() {
	for _, key := range []string{"captcha_enabled", "captcha_register_enabled", "captcha_login_enabled", "captcha_admin_login_enabled", "captcha_register_code_enabled", "captcha_forgot_code_enabled", "captcha_profile_code_enabled", "captcha_phone_login_code_enabled"} {
		c.setFlag(key)
	}
}

func (c *settingsSaveCtx) saveSMS() {
	if c.a.Notifier == nil {
		c.setErr = fmt.Errorf("短信服务不可用")
		return
	}
	if routes := strings.TrimSpace(c.fv("sms_routes")); routes != "" {
		if err := c.a.Notifier.SaveSMSSettings(c.r.Context(), map[string]string{"sms_routes": routes}); err != nil {
			c.setErr = err
		}
		return
	}
	values := map[string]string{
		"sms_provider":         strings.TrimSpace(c.fv("sms_provider")),
		"sms_access_key":       strings.TrimSpace(c.fv("sms_access_key")),
		"sms_username":         strings.TrimSpace(c.fv("sms_username")),
		"sms_sign_name":        strings.TrimSpace(c.fv("sms_sign_name")),
		"sms_template_code":    strings.TrimSpace(c.fv("sms_template_code")),
		"sms_template_content": strings.TrimSpace(c.fv("sms_template_content")),
		"sms_endpoint":         strings.TrimSpace(c.fv("sms_endpoint")),
		"sms_secret_key":       strings.TrimSpace(c.fv("sms_secret_key")),
	}
	for _, key := range []string{"sms_region", "sms_global_access_key", "sms_global_secret_key", "sms_global_sign_name"} {
		values[key] = strings.TrimSpace(c.fv(key))
	}
	input := c.vals
	if input == nil {
		input = map[string]string{}
		for key := range c.r.PostForm {
			input[key] = c.r.PostForm.Get(key)
		}
	}
	for key := range input {
		if key == "settings_section" || key == "_csrf" || key == "csrf_token" {
			continue
		}
		if _, ok := values[key]; !ok {
			c.setErr = fmt.Errorf("短信设置包含未知字段")
			return
		}
	}
	if err := c.a.Notifier.SaveSMSSettings(c.r.Context(), values); err != nil {
		c.setErr = err
	}
}

func (c *settingsSaveCtx) saveManualIdentity() {
	c.setFlag("manual_identity_enabled")
	c.setFlag("manual_identity_requires_verified_phone")
}

// saveExternalCaptcha 校验并保存外部验证码设置；返回 false 表示校验失败且已写响应。
// 互斥校验基于本次表单提交的本地开关状态（而非库中现值）：单人机验证卡界面按最终一致
// 状态分请求提交，这里仅保证「同一场景不在一次提交里同时勾为本地+外部」。
func (c *settingsSaveCtx) saveExternalCaptcha() bool {
	provider := strings.ToLower(strings.TrimSpace(c.fv("captcha_provider")))
	if provider != "" && provider != "geetest" && provider != "vaptcha" && provider != "corptcha" {
		c.fail("外部验证码 provider 无效")
		return false
	}
	localEnabled := c.fv("captcha_enabled") == "1"
	localRegister := localEnabled && c.fv("captcha_register_enabled") == "1"
	localLogin := localEnabled && c.fv("captcha_login_enabled") == "1"
	// 仅注册/登录做互斥（同一动作不至于同时要求本地+外部两道验证码）。
	// 外部「手机验证码登录」与本地「发送验证码」场景不互斥：本地 phone_code 是广义发码场景
	// （找回密码、绑手机、短信登录发码都走它），与“登录发码走外部”并不重叠，且后端判定本就外部优先。
	if (c.fv("external_captcha_register_enabled") == "1" && localRegister) || (c.fv("external_captcha_login_enabled") == "1" && localLogin) {
		c.fail("同一普通场景不能同时启用本地和外部验证码")
		return false
	}
	c.set("captcha_provider", provider)
	c.set("captcha_geetest_id", strings.TrimSpace(c.fv("captcha_geetest_id")))
	c.set("captcha_vaptcha_vid", strings.TrimSpace(c.fv("captcha_vaptcha_vid")))
	c.set("captcha_corptcha_site_key", strings.TrimSpace(c.fv("captcha_corptcha_site_key")))
	for _, key := range []string{"captcha_geetest_key", "captcha_vaptcha_key", "captcha_corptcha_secret"} {
		if value := strings.TrimSpace(c.fv(key)); value != "" {
			c.set(key, value)
		}
	}
	for _, key := range []string{"external_captcha_register_enabled", "external_captcha_login_enabled", "external_captcha_register_code_enabled", "external_captcha_forgot_code_enabled", "external_captcha_profile_code_enabled", "external_captcha_phone_login_code_enabled"} {
		c.setFlag(key)
	}
	return true
}

// saveAutomaticIdentity 校验并保存自动实名插件设置；返回 false 表示校验失败且已写响应。
func (c *settingsSaveCtx) saveAutomaticIdentity() bool {
	provider := strings.ToLower(strings.TrimSpace(c.fv("verification_provider")))
	if provider != "" && provider != "baidu_face" && provider != "leaf_face" && provider != "smapi" && provider != "stay33" {
		c.fail("自动实名 provider 无效")
		return false
	}
	c.set("verification_provider", provider)
	saveSecret := func(key string) {
		if value := strings.TrimSpace(c.fv(key)); value != "" {
			c.set(key, value)
		}
	}
	switch provider {
	case "baidu_face":
		c.set("verification_baidu_api_key", strings.TrimSpace(c.fv("verification_baidu_api_key")))
		c.set("verification_baidu_plan_id", strings.TrimSpace(c.fv("verification_baidu_plan_id")))
		saveSecret("verification_baidu_secret_key")
	case "leaf_face":
		c.set("verification_leaf_app_id", strings.TrimSpace(c.fv("verification_leaf_app_id")))
		c.set("verification_leaf_api_base", strings.TrimSpace(c.fv("verification_leaf_api_base")))
		saveSecret("verification_leaf_app_secret")
	case "smapi":
		c.set("verification_smapi_app_key", strings.TrimSpace(c.fv("verification_smapi_app_key")))
		c.set("verification_smapi_api_url", strings.TrimSpace(c.fv("verification_smapi_api_url")))
		c.set("verification_smapi_product_code", strings.TrimSpace(c.fv("verification_smapi_product_code")))
		saveSecret("verification_smapi_secret_key")
	case "stay33":
		c.set("verification_stay33_api_key", strings.TrimSpace(c.fv("verification_stay33_api_key")))
		c.set("verification_stay33_api_url", strings.TrimSpace(c.fv("verification_stay33_api_url")))
		c.set("verification_stay33_biz_code", strings.TrimSpace(c.fv("verification_stay33_biz_code")))
		saveSecret("verification_stay33_secret_key")
	}
	return true
}

// saveMailAccounts 校验并保存 SMTP 多账号列表与冷却配置（含旧版单组字段迁移）；
// 返回 false 表示校验/写库失败且已写响应。
func (c *settingsSaveCtx) saveMailAccounts() bool {
	s := c.a.Settings
	accountsJSON := strings.TrimSpace(c.fv(mailAccountsKey))
	var accounts []service.MailAccount
	switch {
	case accountsJSON != "":
		if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
			c.fail("邮件账号列表格式无效，请重试")
			return false
		}
	case strings.TrimSpace(c.fv("smtp_host")) != "":
		// 旧版单组字段表单提交 → 转成单个账号
		port := 587
		if p, e := strconv.Atoi(strings.TrimSpace(c.fv("smtp_port"))); e == nil && p > 0 {
			port = p
		}
		accounts = []service.MailAccount{{
			Name: "默认账号", Host: strings.TrimSpace(c.fv("smtp_host")), Port: port,
			User: strings.TrimSpace(c.fv("smtp_user")), Pass: c.fv("smtp_pass"),
			From: strings.TrimSpace(c.fv("smtp_from")), Enabled: true,
		}}
	}
	// 密码留空 = 保留旧值（按 主机+用户名 匹配；首次从旧单组键迁移时回退读取）
	readKey := func(k string) string {
		v, _ := s.Get(c.r.Context(), k)
		return v
	}
	var oldAccounts []service.MailAccount
	if oldRaw, e := s.Get(c.r.Context(), mailAccountsKey); e == nil && strings.TrimSpace(oldRaw) != "" {
		if err := json.Unmarshal([]byte(oldRaw), &oldAccounts); err != nil {
			// 旧列表损坏会使"留空保持旧密码"失效，需留日志排查
			log.Printf("[settings] 旧邮件账号列表解析失败: %v", err)
		}
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
			c.fail(fmt.Sprintf("第 %d 个账号未填写 SMTP 主机", i+1))
			return false
		}
		if a.Port < 1 || a.Port > 65535 {
			c.fail(fmt.Sprintf("第 %d 个账号端口无效", i+1))
			return false
		}
		a.Pass = keepPass(a.Host, a.User, a.Pass)
		clean = append(clean, a)
	}
	storedJSON, _ := json.Marshal(clean)
	if err := s.Set(c.r.Context(), mailAccountsKey, string(storedJSON)); err != nil {
		http.Error(c.w, "保存邮件账号失败", 500)
		return false
	}
	// 失败冷却秒数（默认 60，可配 1~86400）
	cooldown := 60
	if v := strings.TrimSpace(c.fv(mailCooldownKey)); v != "" {
		if p, e := strconv.Atoi(v); e == nil && p >= 1 {
			cooldown = p
			if cooldown > 86400 {
				cooldown = 86400
			}
		}
	}
	if err := s.Set(c.r.Context(), mailCooldownKey, strconv.Itoa(cooldown)); err != nil {
		http.Error(c.w, "保存发送配置失败", 500)
		return false
	}
	// 镜像首个账号到旧单组键（兼容旧读取路径，列表保存后仍可用）
	if len(clean) > 0 {
		first := clean[0]
		c.set("smtp_host", first.Host)
		c.set("smtp_port", strconv.Itoa(first.Port))
		c.set("smtp_user", first.User)
		c.set("smtp_pass", first.Pass)
		c.set("smtp_from", first.From)
	} else {
		c.set("smtp_host", "")
		c.set("smtp_port", "")
		c.set("smtp_user", "")
		c.set("smtp_pass", "")
		c.set("smtp_from", "")
	}
	return true
}

func (a *Admin) adminSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	vals := jsonVals(r)
	if vals == nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "表单解析失败", http.StatusBadRequest)
			return
		}
	}
	c := &settingsSaveCtx{a: a, w: w, r: r, vals: vals}
	switch c.fv("settings_section") {
	case "registration":
		c.saveRegistration()
		c.success()
	case "captcha":
		c.saveCaptcha()
		c.success()
	case "sms":
		c.saveSMS()
		if c.setErr != nil {
			c.fail(c.setErr.Error())
			return
		}
		a.recordSMSChange(r, "sms_settings_saved", "sms_settings", 0, "")
		c.success()
	case "manual_identity":
		c.saveManualIdentity()
		c.success()
	case "lifecycle":
		if c.saveLifecycle() {
			c.success()
		}
	case "external_captcha":
		if c.saveExternalCaptcha() {
			c.success()
		}
	case "automatic_identity":
		if c.saveAutomaticIdentity() {
			c.success()
		}
	default:
		// SMTP/邮件服务（含旧版未带 settings_section 的综合表单）。
		// 业务邮件总开关已移至「邮件模板」页，此处不再写入，避免旧表单把它重置为 0。
		if !c.saveMailAccounts() {
			return
		}
		c.success()
	}
}

// adminTestEmail POST /admin/settings/test-email — 发送测试邮件。
func (a *Admin) adminTestEmail(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	vals, err := bodyValues(r)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "msg": "请求解析失败"})
		return
	}
	to := strings.TrimSpace(vals["email"])
	if to == "" || !strings.Contains(to, "@") {
		writeJSON(w, map[string]any{"ok": false, "msg": "请输入有效的收件邮箱"})
		return
	}
	n := a.Notifier
	if n == nil {
		writeJSON(w, map[string]any{"ok": false, "msg": "邮件服务暂不可用"})
		return
	}
	siteName := n.SiteName(r.Context())
	if siteName == "" {
		siteName = service.DefaultSiteName
	}
	subject := siteName + " 邮件发送测试"
	body := "这是一封来自 " + siteName + " 的测试邮件，收到即表示 SMTP 配置生效。\r\n发送时间：" + time.Now().Format("2006-01-02 15:04:05")
	idx := -1
	if v := strings.TrimSpace(vals["account_index"]); v != "" {
		if p, e := strconv.Atoi(v); e == nil && p >= 0 {
			idx = p
		} else {
			writeJSON(w, map[string]any{"ok": false, "msg": "账号序号无效"})
			return
		}
	}
	if !a.allowEmailTest(w, r) {
		return
	}
	if idx >= 0 {
		err = n.SendTestMailAccount(r.Context(), idx, to, subject, body)
	} else {
		err = n.SendTestMail(r.Context(), to, subject, body)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "msg": "发送失败，请检查收件邮箱及邮件通道配置后重试"})
		return
	}
	if idx >= 0 {
		writeJSON(w, map[string]any{"ok": true, "msg": fmt.Sprintf("测试邮件已通过账号 #%d 发送至 %s", idx+1, to)})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "msg": "测试邮件已发送至 " + to})
}
