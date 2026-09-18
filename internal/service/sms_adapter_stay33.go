package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// stay33Adapter Stay33 短信适配器。
type stay33Adapter struct {
	settings map[string]string
	client   *http.Client
}

func newStay33Adapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &stay33Adapter{settings: settings, client: client}, nil
}

func (p *stay33Adapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("stay33")
	return d
}

func (p *stay33Adapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if !smsProviderSupports("stay33", t.Range, t.Kind) {
		return out, errors.New("短信服务商不支持该范围或类型")
	}
	var err error
	phone, err = smsPhoneForRange(phone, SMSRangeCN)
	if err != nil {
		return out, err
	}
	username, key := p.settings["sms_username"], p.settings["sms_api_key"]
	if username == "" {
		username = p.settings["sms_user"]
	}
	if key == "" {
		key = p.settings["sms_token"]
	}
	if key == "" {
		key = p.settings["sms_secret_key"]
	}
	sign := strings.Trim(t.SignName, "【】")
	if sign == "" {
		sign = strings.Trim(p.settings["sms_sign_name"], "【】")
	}
	if username == "" || key == "" || sign == "" {
		return out, errors.New("Stay33 短信配置不完整")
	}
	endpoint := p.settings["sms_endpoint"]
	if endpoint == "" {
		endpoint = "https://idc.stay33.cn/sms/sendApi.php"
	}
	if !allowedSMSURL(endpoint, "stay33") {
		return out, errors.New("Stay33 短信地址不受支持")
	}
	content := t.Content
	if content == "" {
		content = p.settings["sms_template_content"]
	}
	if content == "" {
		content = "【" + sign + "】您的验证码是：" + values["code"] + "，5分钟内有效。"
	} else {
		code := values["code"]
		purpose := values["purpose"]
		content = strings.ReplaceAll(content, "{{code}}", code)
		content = strings.ReplaceAll(content, "{code}", code)
		content = strings.ReplaceAll(content, "{{purpose}}", purpose)
		content = strings.ReplaceAll(content, "{{sign_name}}", sign)
		content = strings.ReplaceAll(content, "{sign_name}", sign)
	}
	if !smsTextValid(content, 500) {
		return out, errors.New("短信内容含控制字符或过长")
	}
	form := url.Values{"username": {username}, "key": {key}, "phone": {phone}, "content": {content}, "channel": {"1"}}
	body := []byte(form.Encode())
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	raw, err := smsDoRequest(ctx, p.client, http.MethodPost, endpoint, body, headers)
	if err != nil {
		return out, err
	}
	var result struct {
		Code *int `json:"code"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Code == nil {
		return out, smsUnknownError{}
	}
	if *result.Code != 1 {
		return out, errors.New("短信发送失败")
	}
	return out, nil
}

func (p *stay33Adapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("stay33"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *stay33Adapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("stay33"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *stay33Adapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("stay33"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *stay33Adapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("stay33"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func init() {
	registerSMSProvider("stay33", newStay33Adapter)
}
