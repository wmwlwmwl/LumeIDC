package sms

import (
	"lumeidc/internal/smsdk"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// submailAdapter 赛邮（Mailgun 风格 API）适配器。
type submailAdapter struct {
	settings map[string]string
	client   *http.Client
}

func newSubmailAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &submailAdapter{settings: settings, client: client}, nil
}

func (p *submailAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("submail")
	return d
}

func (p *submailAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if !smsProviderSupports("submail", t.Range, t.Kind) {
		return out, errors.New("短信服务商不支持该范围或类型")
	}
	var err error
	phone, err = smsPhoneForRange(phone, t.Range)
	if err != nil {
		return out, err
	}
	signName := t.SignName
	if signName == "" {
		signName = p.settings["sms_sign_name"]
		if t.Range == SMSRangeGlobal {
			signName = p.settings["sms_global_sign_name"]
		}
	}
	form := url.Values{"to": {phone}, "content": {smsSign(signName, t.Content)}}
	if t.Range != SMSRangeGlobal {
		form.Set("to", strings.TrimPrefix(phone, "+86"))
	}
	_, err = p.call(ctx, "send", http.MethodPost, t.Range, form)
	return out, err
}

func (p *submailAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "create", t)
}

func (p *submailAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "update", t)
}

func (p *submailAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "query", SMSProviderTemplate{ID: id, Range: r})
}

func (p *submailAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "delete", SMSProviderTemplate{ID: id, Range: r})
}

func (p *submailAdapter) templateOp(ctx context.Context, action string, t SMSProviderTemplate) (SMSProviderResult, error) {
	out := SMSProviderResult{ProviderTemplateID: t.ID, Status: SMSAuditPending}
	if !p.Descriptor().Capabilities.TemplateCRUD || (t.Range == SMSRangeGlobal) {
		return smsProviderAuditNotSupported("submail"), errors.New("该服务商不支持此范围的远程模板操作，请在供应商控制台管理")
	}
	kind := t.Kind
	if kind == "" {
		kind = "notification"
	}
	if !smsProviderSupports("submail", t.Range, kind) {
		return out, errors.New("短信服务商不支持此范围或类型")
	}
	if action == "create" && t.ID != "" {
		return out, errors.New("模板已有远程编号，禁止重复创建")
	}
	if !smsTextValid(t.Content, 500) || !smsTextValid(t.Name, 100) || !smsTextValid(t.SignName, 100) || !smsTextValid(t.Remark, 500) {
		return out, errors.New("模板字段无效或过长")
	}
	if action != "create" && !smsIdentifier.MatchString(t.ID) {
		return out, errors.New("远程模板编码无效")
	}
	if (action == "create" || action == "update") && strings.TrimSpace(t.Content) == "" {
		return out, errors.New("提交远程模板必须填写正文")
	}
	signName := t.SignName
	if signName == "" {
		signName = p.settings["sms_sign_name"]
	}
	form := url.Values{}
	if action != "create" {
		form.Set("template_id", t.ID)
	}
	if action == "create" || action == "update" {
		form.Set("sms_title", t.Name)
		form.Set("sms_signature", signName)
		form.Set("sms_content", t.Content)
	}
	method := map[string]string{"create": http.MethodPost, "update": http.MethodPut, "query": http.MethodGet, "delete": http.MethodDelete}[action]
	res, err := p.call(ctx, "template", method, t.Range, form)
	if err != nil {
		return out, err
	}
	if action == "create" {
		out.ProviderTemplateID = smsStringValue(res, "template_id")
	}
	if action == "query" {
		s, _ := res["template"].(map[string]any)
		out.Status = smsAudit(smsStringValue(s, "template_status"), 1)
	}
	if action == "create" && !smsIdentifier.MatchString(out.ProviderTemplateID) {
		return out, smsUnknownError{}
	}
	if action == "delete" {
		out.Status = SMSAuditUnknown
		out.ProviderTemplateID = ""
	}
	out.Message = map[SMSAuditStatus]string{
		SMSAuditPending:  "供应商审核中",
		SMSAuditApproved: "供应商审核通过",
		SMSAuditRejected: "供应商审核未通过，请查看供应商控制台",
		SMSAuditUnknown:  "供应商审核状态未知",
	}[out.Status]
	return out, nil
}

func (p *submailAdapter) call(ctx context.Context, action, method string, r SMSRange, form url.Values) (map[string]any, error) {
	prefix := "sms_"
	path := "message/" + action + ".json"
	if r == SMSRangeGlobal {
		prefix = "sms_global_"
		if action == "template" {
			return nil, errors.New("赛邮原插件未提供国际模板管理协议，请在供应商控制台管理")
		}
		path = "internationalsms/send.json"
	}
	if p.settings[prefix+"access_key"] == "" || p.settings[prefix+"secret_key"] == "" {
		return nil, errors.New("赛邮当前范围的应用标识或密钥未配置")
	}
	endpoint := "https://api.mysubmail.com/" + path
	form.Set("appid", p.settings[prefix+"access_key"])
	form.Set("signature", p.settings[prefix+"secret_key"])
	form.Set("appkey", p.settings[prefix+"secret_key"])
	form.Set("timestamp", strconv.FormatInt(time.Now().Unix(), 10))

	body := []byte(form.Encode())
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

	var raw []byte
	var err error
	if method == http.MethodGet {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		raw, err = smsDoRequest(ctx, p.client, http.MethodGet, endpoint+sep+form.Encode(), nil, nil)
	} else {
		raw, err = smsDoRequest(ctx, p.client, method, endpoint, body, headers)
	}
	if err != nil {
		return nil, err
	}
	res, err := decodeSMSResponse(raw)
	if err != nil {
		return nil, err
	}
	status := smsStringValue(res, "status")
	if status == "" {
		return nil, smsUnknownError{}
	}
	if status != "success" {
		_, e := smsRejected()
		return nil, e
	}
	return res, nil
}

var descriptorSubmail = ProviderDescriptor{
	Key: "submail", Name: "赛邮",
	ConfigFields: []string{"sms_access_key", "sms_secret_key", "sms_sign_name", "sms_global_access_key", "sms_global_secret_key", "sms_global_sign_name"},
	Capabilities: SMSProviderCapabilities{Ranges: []SMSRange{SMSRangeCN, SMSRangeGlobal}, OTP: true, Notification: true, TemplateCRUD: true, AuditSync: true},
}

func init() {
	smsdk.RegisterSMSProvider(descriptorSubmail, newSubmailAdapter)
}
