package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// idcsmartAdapter 智简魔方（idcsmart）适配器。
type idcsmartAdapter struct {
	settings map[string]string
	client   *http.Client
	path     string // "smsapi.php" 或 "smsproapi.php"
	key      string // "idcsmart" 或 "idcsmartpro"
}

func newIDCsmartAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &idcsmartAdapter{settings: settings, client: client, path: "smsapi.php", key: "idcsmart"}, nil
}

func newIDCsmartProAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &idcsmartAdapter{settings: settings, client: client, path: "smsproapi.php", key: "idcsmartpro"}, nil
}

func (p *idcsmartAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor(p.key)
	return d
}

func (p *idcsmartAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if !smsProviderSupports(p.key, t.Range, t.Kind) {
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
	}
	form := url.Values{"to": {phone}, "content": {smsSign(signName, t.Content)}}
	if t.Range != SMSRangeGlobal {
		form.Set("to", strings.TrimPrefix(phone, "+86"))
	}
	form.Set("template_id", t.ID)
	for k, v := range values {
		form.Set("vars["+k+"]", v)
	}
	_, err = p.call(ctx, "send", http.MethodPost, form)
	return out, err
}

func (p *idcsmartAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "create", t)
}

func (p *idcsmartAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "update", t)
}

func (p *idcsmartAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "query", SMSProviderTemplate{ID: id, Range: r})
}

func (p *idcsmartAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "delete", SMSProviderTemplate{ID: id, Range: r})
}

func (p *idcsmartAdapter) templateOp(ctx context.Context, action string, t SMSProviderTemplate) (SMSProviderResult, error) {
	out := SMSProviderResult{ProviderTemplateID: t.ID, Status: SMSAuditPending}
	if !p.Descriptor().Capabilities.TemplateCRUD {
		return smsProviderAuditNotSupported(p.key), errors.New("该服务商不支持此范围的远程模板操作，请在供应商控制台管理")
	}
	kind := t.Kind
	if kind == "" {
		kind = "notification"
	}
	if !smsProviderSupports(p.key, t.Range, kind) {
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
		form.Set("title", t.Name)
		form.Set("signature", smsSign(signName, ""))
		form.Set("content", t.Content)
	}
	method := map[string]string{"create": http.MethodPost, "update": http.MethodPut, "query": http.MethodGet, "delete": http.MethodDelete}[action]
	res, err := p.call(ctx, "template", method, form)
	if err != nil {
		return out, err
	}
	if action == "create" {
		out.ProviderTemplateID = smsStringValue(res, "template_id")
	}
	if action == "query" {
		s, _ := res["template"].(map[string]any)
		out.Status = smsAudit(smsStringValue(s, "status"), 1)
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

func (p *idcsmartAdapter) call(ctx context.Context, action, method string, form url.Values) (map[string]any, error) {
	if p.settings["sms_username"] == "" || p.settings["sms_secret_key"] == "" {
		return nil, errors.New("智简短信账号或密钥未配置")
	}
	endpoint := "https://api1.idcsmart.com/" + p.path
	headers := map[string]string{
		"api": p.settings["sms_username"],
		"key": p.settings["sms_secret_key"],
	}
	checked, err := smsHTTPSURL(p.settings["sms_endpoint"], endpoint)
	if err != nil {
		return nil, err
	}
	endpoint = checked + "?action=" + action

	body := []byte(form.Encode())
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	var raw []byte
	if method == http.MethodGet {
		raw, err = smsDoRequest(ctx, p.client, http.MethodGet, endpoint+"&"+form.Encode(), nil, headers)
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
	if status != "200" {
		_, e := smsRejected()
		return nil, e
	}
	return res, nil
}

func init() {
	registerSMSProvider("idcsmart", newIDCsmartAdapter)
	registerSMSProvider("idcsmartpro", newIDCsmartProAdapter)
}
