package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// smsbaoAdapter 短信宝适配器。
type smsbaoAdapter struct {
	settings map[string]string
	client   *http.Client
}

func newSMSBaoAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &smsbaoAdapter{settings: settings, client: client}, nil
}

func (p *smsbaoAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("smsbao")
	return d
}

func (p *smsbaoAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if !smsProviderSupports("smsbao", t.Range, t.Kind) {
		return out, errors.New("短信服务商不支持该范围或类型")
	}
	var err error
	phone, err = smsPhoneForRange(phone, t.Range)
	if err != nil {
		return out, err
	}
	path := "/sms"
	if t.Range == SMSRangeGlobal {
		path = "/wsms"
	} else {
		phone = strings.TrimPrefix(phone, "+86")
	}
	endpoint, err := smsHTTPSURL(p.settings["sms_endpoint"], "https://api.smsbao.com"+path)
	if err != nil {
		return out, err
	}
	if p.settings["sms_username"] == "" || p.settings["sms_secret_key"] == "" {
		return out, errors.New("短信宝账号或密码未配置")
	}
	signName := t.SignName
	if signName == "" {
		signName = p.settings["sms_sign_name"]
	}
	form := url.Values{"u": {p.settings["sms_username"]}, "p": {md5Hex(p.settings["sms_secret_key"])}, "m": {phone}, "c": {smsSign(signName, t.Content)}}
	raw, err := smsDoRequest(ctx, p.client, http.MethodGet, endpoint+"?"+form.Encode(), nil, nil)
	if err != nil {
		return out, err
	}
	code := strings.TrimSpace(string(raw))
	if code != "0" {
		switch code {
		case "30", "40", "41", "42", "43", "50", "51", "-1", "-2", "18446744073709551615", "18446744073709551614":
			return smsRejected()
		default:
			return out, smsUnknownError{}
		}
	}
	return out, nil
}

func (p *smsbaoAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("smsbao"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *smsbaoAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("smsbao"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *smsbaoAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("smsbao"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func (p *smsbaoAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return smsProviderAuditNotSupported("smsbao"), errors.New("该服务商不支持远程模板操作，请在供应商控制台管理")
}

func init() {
	registerSMSProvider("smsbao", newSMSBaoAdapter)
}
