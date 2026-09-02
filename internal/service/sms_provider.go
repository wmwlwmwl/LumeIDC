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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// ConfiguredSMSProvider 内置实现 TuraIDC/ZJMF 的短信 provider 语义。
// provider 只能取 aliyun、aliyun_sms 或 stay33；不执行任意上传代码。
type ConfiguredSMSProvider struct {
	Settings *repo.Settings
	Client   *http.Client
}

func NewConfiguredSMSProvider(settings *repo.Settings) *ConfiguredSMSProvider {
	return &ConfiguredSMSProvider{Settings: settings, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (p *ConfiguredSMSProvider) Send(ctx context.Context, phone, code string) error {
	return p.SendPurpose(ctx, phone, code, "verify_phone")
}

func (p *ConfiguredSMSProvider) SendPurpose(ctx context.Context, phone, code, purpose string) error {
	if p == nil || p.Settings == nil {
		return errors.New("短信 provider 未配置")
	}
	get := func(key string) string {
		v, _ := p.Settings.Get(ctx, key)
		return strings.TrimSpace(v)
	}
	provider := strings.ToLower(get("sms_provider"))
	switch provider {
	case "aliyun":
		return p.sendAliyunPNVS(ctx, phone, code, purpose, get)
	case "aliyun_sms":
		return p.sendAliyunSMS(ctx, phone, code, purpose, get)
	case "stay33":
		return p.sendStay33(ctx, phone, code, purpose, get)
	default:
		return errors.New("短信 provider 未配置或不受支持")
	}
}

type settingGetter func(string) string

func (p *ConfiguredSMSProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
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
	form := url.Values{"username": {username}, "key": {key}, "phone": {phone}, "content": {content}, "channel": {"1"}}
	return p.postForm(ctx, endpoint, form, func(body []byte) error {
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return errors.New("短信服务响应无效")
		}
		if result.Code != 1 {
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
	params := map[string]string{
		"Action": "SendSmsVerifyCode", "SchemeName": "默认方案", "CountryCode": "86",
		"PhoneNumber": phone, "SignName": strings.Trim(get("sms_sign_name"), "【】"),
		"TemplateCode": templateCode, "TemplateParam": fmt.Sprintf(`{"code":"%s"}`, code),
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
	host := get("sms_endpoint")
	if host == "" {
		host = "dysmsapi.aliyuncs.com"
	}
	if !allowedSMSURL("https://"+host, "aliyun_sms") {
		return errors.New("阿里云短信地址不受支持")
	}
	query := map[string]string{"PhoneNumbers": phone, "SignName": sign, "TemplateCode": templateCode, "TemplateParam": fmt.Sprintf(`{"code":"%s"}`, code)}
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
		return errors.New("阿里云短信服务连接失败")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("阿里云短信服务请求失败")
	}
	var result struct {
		Code string `json:"Code"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code != "OK" {
		return errors.New("阿里云短信发送失败")
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
		return errors.New("短信服务连接失败")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("短信服务请求失败")
	}
	return check(body)
}

func allowedSMSURL(raw, provider string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
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
