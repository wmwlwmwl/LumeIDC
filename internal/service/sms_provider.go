package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// ConfiguredSMSProvider 是短信发送的业务入口。
// 所有供应商差异通过 NewSMSProvider() 创建的适配器处理，自身不包含第三方协议逻辑。
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

// sendPurposeWithSettings 统一走适配器：构造模板 → 渲染 → NewSMSProvider → SendSMS。
// 消除了旧版按供应商分发 sendAliyunPNVS/sendAliyunSMS/sendStay33 的 switch-case。
func (p *ConfiguredSMSProvider) sendPurposeWithSettings(ctx context.Context, phone, code, purpose string, settings map[string]string) error {
	if _, err := smsScene("otp_" + purpose); err != nil {
		return err
	}
	phone, err := smsPhoneForRange(phone, SMSRangeCN)
	if err != nil {
		return err
	}
	provider := settings["sms_provider"]
	scene, _ := smsScene("otp_" + purpose)
	t := SMSTemplate{
		Name: "默认验证码", Provider: provider, Kind: "otp", RangeType: SMSRangeCN,
		TemplateCode: settings["sms_template_code"],
		Content:      settings["sms_template_content"],
		SignName:     settings["sms_sign_name"],
	}
	if provider == "qcloudsms" {
		t.Parameters = map[string]string{"1": "code"}
	}
	preview, err := renderSMSTemplate(t, scene, map[string]string{"code": code, "purpose": purpose, "sign_name": settings["sms_sign_name"], "site_name": DefaultSiteName})
	if err != nil {
		return err
	}
	if err = smsTemplateSendable(t); err != nil {
		return err
	}
	adapter, err := NewSMSProvider(provider, settings, p.Client)
	if err != nil {
		return err
	}
	_, err = adapter.SendSMS(ctx, phone, SMSProviderTemplate{
		ID: preview.TemplateCode, Content: preview.Content,
		SignName: preview.SignName, Kind: "otp", Range: SMSRangeCN,
	}, preview.Parameters)
	return err
}

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

// sendRenderedResult 统一走适配器：不再按供应商分发旧方法。
func (p *ConfiguredSMSProvider) sendRenderedResult(ctx context.Context, phone string, preview SMSPreview, settings map[string]string, otp bool) (SMSProviderResult, error) {
	if preview.Provider != settings["sms_provider"] {
		return SMSProviderResult{ProviderCode: "route_mismatch"}, errors.New("模板与当前短信服务商不匹配")
	}
	if preview.Provider == "aliyun" && !otp {
		return SMSProviderResult{ProviderCode: "otp_only"}, errors.New("号码认证不支持业务通知")
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
	kind := "notification"
	if otp {
		kind = "otp"
	}
	adapter, err := NewSMSProvider(preview.Provider, settings, p.Client)
	if err != nil {
		return SMSProviderResult{ProviderCode: "provider_invalid"}, err
	}
	return adapter.SendSMS(ctx, normalized, SMSProviderTemplate{
		ID: preview.TemplateCode, Content: preview.Content,
		SignName: preview.SignName, Kind: kind, Range: preview.RangeType,
	}, preview.Parameters)
}

// SMSServiceConfigured 判断是否登记了受支持的短信服务商（全站全局状态，
// 不含用户手机号等隐私信息，可在防枚举短路前安全判定）。
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
