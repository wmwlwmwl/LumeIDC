package sms

import (
	"lumeidc/internal/smsdk"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// aliyunAdapter 阿里云号码认证（DYPNS）适配器。
// 仅支持中国大陆手机号验证码（短信验证回退）。
type aliyunAdapter struct {
	settings map[string]string
	client   *http.Client
}

func newAliyunAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &aliyunAdapter{settings: settings, client: client}, nil
}

func (p *aliyunAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("aliyun")
	return d
}

func (p *aliyunAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if t.Kind != "otp" {
		return out, errors.New("阿里云号码认证仅支持验证码短信")
	}
	if t.Range != "" && t.Range != SMSRangeCN {
		return out, errors.New("阿里云号码认证仅支持中国大陆手机号")
	}
	phone, err := smsPhoneForRange(phone, SMSRangeCN)
	if err != nil {
		return out, err
	}
	code := values["code"]
	purpose := values["purpose"]
	if code == "" {
		return out, errors.New("验证码不能为空")
	}
	return out, p.sendOTP(ctx, phone, code, purpose, t)
}

// sendOTP 调用阿里云号码认证短信发送接口（SendSmsVerifyCode）。
// 与原 ConfiguredSMSProvider.sendAliyun 行为完全一致。
func (p *aliyunAdapter) sendOTP(ctx context.Context, phone, code, purpose string, t SMSProviderTemplate) error {
	accessKey := p.accessKey()
	secret := p.secretKey()
	sign := t.SignName
	if sign == "" {
		sign = p.settings["sms_sign_name"]
	}
	sign = strings.Trim(sign, "【】")
	templateCode := t.ID
	if templateCode == "" {
		templateCode = p.otpTemplateCode(purpose)
	}
	if accessKey == "" || secret == "" || sign == "" || templateCode == "" {
		return errors.New("阿里云号码认证配置不完整")
	}
	if _, err := smsHTTPSURL(p.settings["sms_endpoint"], "https://dypnsapi.aliyuncs.com/"); err != nil {
		return err
	}
	host := "dypnsapi.aliyuncs.com"
	endpoint := "https://" + host + "/"

	codeJSON, _ := json.Marshal(map[string]string{"code": code})

	query := map[string]string{
		"Action":         "SendSmsVerifyCode",
		"SchemeName":     "默认方案",
		"CountryCode":    "86",
		"PhoneNumber":    strings.TrimPrefix(phone, "+86"),
		"SignName":       sign,
		"TemplateCode":   templateCode,
		"TemplateParam":  string(codeJSON),
		"CodeLength":     "6",
		"ValidTime":      "300",
		"CodeType":       "1",
		"AutoRetry":      "1",
		"Format":         "json",
		"Version":        "2017-05-25",
		"AccessKeyId":    accessKey,
		"SignatureMethod": "HMAC-SHA256",
		"SignatureVersion": "1.0",
		"SignatureNonce": randomHex(16),
		"Timestamp":      time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"RegionId":       "cn-hangzhou",
	}

	canonical := canonicalQuery(query)
	stringToSign := "POST&%2F&" + percentEncode(canonical)
	signature := base64.StdEncoding.EncodeToString(hmacSHA256([]byte(secret+"&"), []byte(stringToSign)))

	// 构造 POST form body（含 Signature）
	formParts := make([]string, 0, len(query)+1)
	formParts = append(formParts, "Signature="+percentEncode(signature))
	for k, v := range query {
		formParts = append(formParts, percentEncode(k)+"="+percentEncode(v))
	}
	bodyStr := strings.Join(formParts, "&")

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}
	raw, err := smsDoRequest(ctx, p.client, http.MethodPost, endpoint, []byte(bodyStr), headers)
	if err != nil {
		return err
	}
	var result struct {
		Code    string `json:"Code"`
		Success bool   `json:"Success"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Code == "" {
		return smsUnknownError{}
	}
	if result.Code != "OK" || !result.Success {
		return errors.New("阿里云短信发送失败")
	}
	return nil
}

func (p *aliyunAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("aliyun"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *aliyunAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("aliyun"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *aliyunAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("aliyun"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *aliyunAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("aliyun"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *aliyunAdapter) accessKey() string {
	if v := p.settings["sms_access_key"]; v != "" {
		return v
	}
	return p.settings["sms_user"]
}

func (p *aliyunAdapter) secretKey() string {
	if v := p.settings["sms_secret_key"]; v != "" {
		return v
	}
	if v := p.settings["sms_token"]; v != "" {
		return v
	}
	return ""
}

func (p *aliyunAdapter) otpTemplateCode(purpose string) string {
	switch strings.ToLower(purpose) {
	case "login", "register":
		return "100001"
	case "change":
		return "100002"
	case "bind":
		return "100004"
	case "verify_phone", "verify_bound_phone":
		return "100005"
	}
	if v := p.settings["sms_template_code"]; v != "" {
		return v
	}
	return "100005"
}

// 描述符与实现同文件内聚（新增服务商照此声明 + 注册）。
var descriptorAliyun = ProviderDescriptor{
	Key: "aliyun", Name: "阿里云号码认证",
	ConfigFields: []string{"sms_access_key", "sms_secret_key", "sms_sign_name"},
	Capabilities: SMSProviderCapabilities{Ranges: []SMSRange{SMSRangeCN}, OTP: true},
}

func init() {
	smsdk.RegisterSMSProvider(descriptorAliyun, newAliyunAdapter)
}
