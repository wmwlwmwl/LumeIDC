package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// ConfiguredSMSProvider 内置实现 LumeIDC/ZJMF 的短信 provider 语义。
// provider 只能取 aliyun、aliyun_sms 或 stay33；不执行任意上传代码。
type smsSettings interface {
	Get(context.Context, string) (string, error)
	GetMany(context.Context, ...string) (map[string]string, error)
}

type ConfiguredSMSProvider struct {
	Settings smsSettings
	Client   *http.Client
}

func NewConfiguredSMSProvider(settings smsSettings) *ConfiguredSMSProvider {
	return &ConfiguredSMSProvider{Settings: settings, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (p *ConfiguredSMSProvider) SendPurpose(ctx context.Context, phone, code, purpose string) error {
	if _, err := smsScene("otp_" + purpose); err != nil {
		return err
	}
	settings, err := p.loadSettings(ctx)
	if err != nil {
		return err
	}
	return p.sendPurposeWithSettings(ctx, phone, code, purpose, settings)
}

// sendPurposeWithSettings 复用验证码协议分发，但显式使用指定国内路由的配置。
func (p *ConfiguredSMSProvider) sendPurposeWithSettings(ctx context.Context, phone, code, purpose string, settings map[string]string) error {
	if _, err := smsScene("otp_" + purpose); err != nil {
		return err
	}
	var err error
	phone, err = smsPhoneForRange(phone, SMSRangeCN)
	if err != nil {
		return err
	}
	get := func(key string) string { return settings[key] }
	provider := get("sms_provider")
	switch provider {
	case "aliyun":
		return p.sendAliyunPNVS(ctx, phone, code, purpose, get)
	case "aliyun_sms":
		return p.sendAliyunSMS(ctx, phone, code, purpose, get)
	case "stay33":
		return p.sendStay33(ctx, phone, code, purpose, get)
	case "qcloudsms", "submail", "smsbao", "idcsmart":
		scene, _ := smsScene("otp_" + purpose)
		t := SMSTemplate{Name: "默认验证码", Provider: provider, Kind: "otp", RangeType: SMSRangeCN, TemplateCode: get("sms_template_code"), Content: get("sms_template_content"), SignName: get("sms_sign_name")}
		if provider == "qcloudsms" {
			t.Parameters = map[string]string{"1": "code"}
		}
		preview, err := renderSMSTemplate(t, scene, map[string]string{"code": code, "purpose": purpose, "sign_name": get("sms_sign_name"), "site_name": DefaultSiteName})
		if err != nil {
			return err
		}
		if err = smsTemplateSendable(t); err != nil {
			return err
		}
		return p.sendRendered(ctx, phone, preview, settings, true)
	default:
		return errors.New("短信服务商未配置或不支持验证码")
	}
}

type settingGetter func(string) string

type smsUnknownError struct{}

func (smsUnknownError) Error() string { return "短信发送结果未确认，请勿自动重发" }

var smsSettingKeys = []string{"sms_region", "sms_global_access_key", "sms_global_secret_key", "sms_global_sign_name", "sms_provider", "sms_username", "sms_api_key", "sms_user", "sms_token", "sms_secret_key", "sms_access_key", "sms_sign_name", "sms_endpoint", "sms_template_code", "sms_template_content"}

func (p *ConfiguredSMSProvider) loadSettings(ctx context.Context) (map[string]string, error) {
	if p == nil || p.Settings == nil {
		return nil, errors.New("短信服务未配置")
	}
	values, err := p.Settings.GetMany(ctx, smsSettingKeys...)
	if err != nil {
		return nil, errors.New("读取短信配置失败")
	}
	for k, v := range values {
		values[k] = strings.TrimSpace(v)
	}
	values["sms_provider"] = strings.ToLower(values["sms_provider"])
	return values, nil
}

// SendPayload 仅普通短信，使用当前单组凭据；不复用验证码用途路由。
func (p *ConfiguredSMSProvider) SendPayload(ctx context.Context, phone string, payload smsPayload) error {
	settings, err := p.loadSettings(ctx)
	if err != nil {
		return err
	}
	if payload.Template.Kind != "notification" || payload.Preview.Provider == "aliyun" {
		return errors.New("号码认证不支持业务通知")
	}
	if err := smsTemplateSendable(payload.Template); err != nil {
		return err
	}
	settings["sms_sign_name"] = payload.SignName
	return p.sendRendered(ctx, phone, payload.Preview, settings, false)
}
func (p *ConfiguredSMSProvider) sendRendered(ctx context.Context, phone string, preview SMSPreview, settings map[string]string, otp bool) error {
	_, err := p.sendRenderedResult(ctx, phone, preview, settings, otp)
	return err
}

// sendRenderedResult 为队列保留供应商返回的请求编号和消息流水；旧调用继续使用 sendRendered。
func (p *ConfiguredSMSProvider) sendRenderedResult(ctx context.Context, phone string, preview SMSPreview, settings map[string]string, otp bool) (SMSProviderResult, error) {
	if preview.Provider != settings["sms_provider"] {
		return SMSProviderResult{ProviderCode: "route_mismatch"}, errors.New("模板与当前短信服务商不匹配")
	}
	normalized, err := smsPhoneForRange(phone, preview.RangeType)
	if err != nil {
		return SMSProviderResult{ProviderCode: "invalid_phone"}, err
	}
	if preview.SignName != "" {
		settings["sms_sign_name"] = preview.SignName
	}
	if !smsTextValid(settings["sms_sign_name"], 100) {
		return SMSProviderResult{ProviderCode: "invalid_sign"}, errors.New("短信签名无效")
	}
	raw, _ := json.Marshal(preview.Parameters)
	get := func(key string) string {
		switch key {
		case "sms_template_code", "sms_bound_template_code":
			return preview.TemplateCode
		case "sms_rendered_content":
			return preview.Content
		case "sms_rendered_parameters":
			return string(raw)
		}
		return settings[key]
	}
	switch preview.Provider {
	case "aliyun":
		if !otp {
			return SMSProviderResult{ProviderCode: "otp_only"}, errors.New("号码认证不支持业务通知")
		}
		err = p.sendAliyunPNVS(ctx, normalized, preview.Parameters["code"], "", get)
	case "aliyun_sms":
		err = p.sendAliyunSMS(ctx, normalized, "", "", get)
	case "stay33":
		err = p.sendStay33(ctx, normalized, "", "", get)
	default:
		kind := "notification"
		if otp {
			kind = "otp"
		}
		provider, createErr := NewSMSProvider(preview.Provider, settings, p.Client)
		if createErr != nil {
			return SMSProviderResult{ProviderCode: "provider_invalid"}, createErr
		}
		return provider.SendSMS(ctx, normalized, SMSProviderTemplate{ID: preview.TemplateCode, Content: preview.Content, SignName: preview.SignName, Kind: kind, Range: preview.RangeType}, preview.Parameters)
	}
	if err != nil {
		return SMSProviderResult{ProviderCode: "provider_error"}, err
	}
	return SMSProviderResult{}, nil
}

// SMSServiceConfigured 判断是否登记了受支持的短信服务商（全站全局状态，
// 不含用户手机号等隐私信息，可在防枚举短路前安全判定）。
// sms_provider 取值白名单须与 ConfiguredSMSProvider.SendPurpose 保持一致。
func SMSServiceConfigured(ctx context.Context, s *repo.Settings) bool {
	if s == nil {
		return false
	}
	raw, _ := s.Get(ctx, "sms_routes")
	if routes, err := parseSMSRoutes(raw); err == nil {
		if route, ok := routes[SMSRangeCN]; ok {
			provider, _, err := smsRouteConfig(route, SMSRangeCN)
			return err == nil && provider != ""
		}
	}
	provider, _ := s.Get(ctx, "sms_provider")
	_, ok := SMSProviderDescriptorFor(strings.ToLower(strings.TrimSpace(provider)))
	return ok
}

func (p *ConfiguredSMSProvider) client() *http.Client {
	client := http.Client{Timeout: 15 * time.Second}
	if p.Client != nil {
		client = *p.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	return &client
}

func (p *ConfiguredSMSProvider) sendStay33(ctx context.Context, phone, code, purpose string, get settingGetter) error {
	username, key := get("sms_username"), get("sms_api_key")
	if username == "" {
		username = get("sms_user")
	}
	if key == "" {
		key = get("sms_token")
	}
	if key == "" {
		key = get("sms_secret_key")
	}
	sign := strings.Trim(get("sms_sign_name"), "【】")
	if username == "" || key == "" || sign == "" {
		return errors.New("Stay33 短信配置不完整")
	}
	endpoint := get("sms_endpoint")
	if endpoint == "" {
		endpoint = "https://idc.stay33.cn/sms/sendApi.php"
	}
	if !allowedSMSURL(endpoint, "stay33") {
		return errors.New("Stay33 短信地址不受支持")
	}
	content := get("sms_template_content")
	if content == "" {
		content = "【" + sign + "】您的验证码是：" + code + "，5分钟内有效。"
	} else {
		content = strings.ReplaceAll(content, "{{code}}", code)
		content = strings.ReplaceAll(content, "{code}", code)
		content = strings.ReplaceAll(content, "{{purpose}}", purpose)
		content = strings.ReplaceAll(content, "{{sign_name}}", sign)
		content = strings.ReplaceAll(content, "{sign_name}", sign)
	}
	if exact := get("sms_rendered_content"); exact != "" {
		content = exact
	}
	if !smsTextValid(content, 500) {
		return errors.New("短信内容含控制字符或过长")
	}
	form := url.Values{"username": {username}, "key": {key}, "phone": {phone}, "content": {content}, "channel": {"1"}}
	return p.postForm(ctx, endpoint, form, func(body []byte) error {
		var result struct {
			Code *int `json:"code"`
		}
		if err := json.Unmarshal(body, &result); err != nil || result.Code == nil {
			return smsUnknownError{}
		}
		if *result.Code != 1 {
			return errors.New("短信发送失败")
		}
		return nil
	})
}

func (p *ConfiguredSMSProvider) sendAliyunPNVS(ctx context.Context, phone, code, purpose string, get settingGetter) error {
	accessKey, secret := get("sms_access_key"), get("sms_secret_key")
	if accessKey == "" {
		accessKey = get("sms_user")
	}
	if secret == "" {
		secret = get("sms_token")
	}
	if secret == "" {
		secret = get("sms_secret_key")
	}
	if accessKey == "" || secret == "" {
		return errors.New("阿里云号码认证配置不完整")
	}
	templateCode := map[string]string{"login": "100001", "register": "100001", "change": "100002", "bind": "100004", "verify_phone": "100005", "verify_bound_phone": "100005"}[strings.ToLower(purpose)]
	if templateCode == "" {
		templateCode = get("sms_template_code")
	}
	if templateCode == "" {
		templateCode = "100005"
	}
	if override := get("sms_bound_template_code"); override != "" {
		templateCode = override
	}
	codeJSON, _ := json.Marshal(map[string]string{"code": code})
	// PNVS 保留自定义验证码与本地HMAC验证语义；云端协议尚未真实联调。
	params := map[string]string{
		"Action": "SendSmsVerifyCode", "SchemeName": "默认方案", "CountryCode": "86",
		"PhoneNumber": phone, "SignName": strings.Trim(get("sms_sign_name"), "【】"),
		"TemplateCode": templateCode, "TemplateParam": string(codeJSON),
		"CodeLength": "6", "ValidTime": "300", "CodeType": "1", "AutoRetry": "1",
	}
	query := map[string]string{"Format": "json", "RegionId": "cn-hangzhou", "SignatureMethod": "HMAC-SHA256", "SignatureNonce": randomHex(16), "SignatureVersion": "1.0", "Timestamp": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "Version": "2017-05-25", "AccessKeyId": accessKey}
	for k, v := range params {
		query[k] = v
	}
	canonical := canonicalQuery(query)
	stringToSign := "POST&%2F&" + percentEncode(canonical)
	sig := base64.StdEncoding.EncodeToString(hmacSHA256([]byte(secret+"&"), []byte(stringToSign)))
	form := url.Values{}
	form.Set("Signature", sig)
	for k, v := range query {
		form.Set(k, v)
	}
	return p.postForm(ctx, "https://dypnsapi.aliyuncs.com/", form, func(body []byte) error {
		var result struct {
			Code    string `json:"Code"`
			Success bool   `json:"Success"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return errors.New("短信服务响应无效")
		}
		if result.Code != "OK" || !result.Success {
			return errors.New("阿里云短信发送失败")
		}
		return nil
	})
}

func (p *ConfiguredSMSProvider) sendAliyunSMS(ctx context.Context, phone, code, purpose string, get settingGetter) error {
	accessKey, secret := get("sms_access_key"), get("sms_secret_key")
	sign, templateCode := strings.Trim(get("sms_sign_name"), "【】"), get("sms_template_code")
	if accessKey == "" || secret == "" || sign == "" || templateCode == "" {
		return errors.New("阿里云短信服务配置不完整")
	}
	if _, err := smsHTTPSURL(get("sms_endpoint"), "https://dysmsapi.aliyuncs.com/"); err != nil {
		return err
	}
	host := "dysmsapi.aliyuncs.com"
	codeJSON, _ := json.Marshal(map[string]string{"code": code})
	parameters := string(codeJSON)
	if exact := get("sms_rendered_parameters"); exact != "" {
		parameters = exact
	}
	query := map[string]string{"PhoneNumbers": phone, "SignName": sign, "TemplateCode": templateCode, "TemplateParam": parameters}
	canonical := canonicalQuery(query)
	contentHash := sha256Hex(nil)
	headers := map[string]string{"host": host, "x-acs-action": "SendSms", "x-acs-content-sha256": contentHash, "x-acs-date": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "x-acs-signature-nonce": randomHex(16), "x-acs-version": "2017-05-25"}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var signed, canonicalHeaders strings.Builder
	for i, k := range keys {
		signed.WriteString(k)
		if i < len(keys)-1 {
			signed.WriteByte(';')
		}
		canonicalHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	canonicalRequest := "POST\n/\n" + canonical + "\n" + canonicalHeaders.String() + "\n" + signed.String() + "\n" + contentHash
	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))
	signature := hex.EncodeToString(hmacSHA256([]byte(secret), []byte(stringToSign)))
	auth := "ACS3-HMAC-SHA256 Credential=" + accessKey + ",SignedHeaders=" + signed.String() + ",Signature=" + signature
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/?"+canonical, nil)
	if err != nil {
		return errors.New("阿里云短信请求无效")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", auth)
	resp, err := p.client().Do(req)
	if err != nil {
		return smsUnknownError{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return smsUnknownError{}
	}
	var result struct {
		Code string `json:"Code"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code == "" {
		return smsUnknownError{}
	}
	if result.Code != "OK" {
		return errors.New("短信供应商明确拒绝请求")
	}
	return nil
}

func (p *ConfiguredSMSProvider) postForm(ctx context.Context, endpoint string, form url.Values, check func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("短信请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return smsUnknownError{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return smsUnknownError{}
	}
	return check(body)
}

func allowedSMSURL(raw, provider string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Port() != "" || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch provider {
	case "aliyun":
		return host == "dypnsapi.aliyuncs.com"
	case "aliyun_sms":
		return host == "dysmsapi.aliyuncs.com"
	case "stay33":
		return host == "idc.stay33.cn" || host == "api.freescdn.com"
	default:
		return false
	}
}

func canonicalQuery(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(percentEncode(k))
		b.WriteByte('=')
		b.WriteString(percentEncode(values[k]))
	}
	return b.String()
}

func percentEncode(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(v), "+", "%20"), "%7E", "~")
}

func hmacSHA256(key, value []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(value)
	return m.Sum(nil)
}

func sha256Hex(value []byte) string {
	h := sha256.Sum256(value)
	return hex.EncodeToString(h[:])
}

func randomHex(size int) string {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
