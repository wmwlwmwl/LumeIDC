package sms

import (
	"lumeidc/internal/smsdk"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// aliyunSMSAdapter 阿里云短信服务适配器。
// 仅负责协议对接，不含业务规则。
type aliyunSMSAdapter struct {
	settings map[string]string
	client   *http.Client
}

func newAliyunSMSAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &aliyunSMSAdapter{settings: settings, client: client}, nil
}

func (p *aliyunSMSAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("aliyun_sms")
	return d
}

func (p *aliyunSMSAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	if !smsProviderSupports("aliyun_sms", t.Range, t.Kind) {
		return SMSProviderResult{}, errors.New("短信服务商不支持该范围或类型")
	}
	phone, err := smsPhoneForRange(phone, t.Range)
	if err != nil {
		return SMSProviderResult{}, err
	}
	signName := t.SignName
	if signName == "" {
		signName = p.settings["sms_sign_name"]
	}
	templateCode := t.ID
	if templateCode == "" {
		return SMSProviderResult{}, errors.New("阿里云短信模板编码未配置")
	}
	if p.settings["sms_access_key"] == "" || p.settings["sms_secret_key"] == "" || signName == "" {
		return SMSProviderResult{}, errors.New("阿里云短信服务配置不完整")
	}
	if _, err := smsHTTPSURL(p.settings["sms_endpoint"], "https://dysmsapi.aliyuncs.com/"); err != nil {
		return SMSProviderResult{}, err
	}
	parameters := string(templateParamsJSON(values))
	_, sendErr := p.call(ctx, "SendSms", map[string]string{
		"PhoneNumbers":  phone,
		"SignName":      strings.Trim(signName, "【】"),
		"TemplateCode":  templateCode,
		"TemplateParam": parameters,
	})
	return SMSProviderResult{}, sendErr
}

func (p *aliyunSMSAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "create", t)
}

func (p *aliyunSMSAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "update", t)
}

func (p *aliyunSMSAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "query", SMSProviderTemplate{ID: id, Range: r})
}

func (p *aliyunSMSAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "delete", SMSProviderTemplate{ID: id, Range: r})
}

func (p *aliyunSMSAdapter) templateOp(ctx context.Context, action string, t SMSProviderTemplate) (SMSProviderResult, error) {
	out := SMSProviderResult{ProviderTemplateID: t.ID, Status: SMSAuditPending}
	if !p.Descriptor().Capabilities.TemplateCRUD {
		return smsProviderAuditNotSupported("aliyun_sms"), errors.New("该服务商不支持此范围的远程模板操作，请在供应商控制台管理")
	}
	kind := t.Kind
	if kind == "" {
		kind = "notification"
	}
	if !smsProviderSupports("aliyun_sms", t.Range, kind) {
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
	actions := map[string]string{
		"create": "AddSmsTemplate", "update": "ModifySmsTemplate",
		"query": "QuerySmsTemplate", "delete": "DeleteSmsTemplate",
	}
	params := map[string]string{}
	if action != "create" {
		params["TemplateCode"] = t.ID
	}
	if action == "create" || action == "update" {
		typ := "1"
		if t.Kind == "otp" {
			typ = "0"
		}
		if t.Range == SMSRangeGlobal {
			typ = "3"
		}
		params["TemplateType"] = typ
		params["TemplateName"] = t.Name
		params["TemplateContent"] = t.Content
		params["Remark"] = t.Remark
	}
	res, err := p.call(ctx, actions[action], params)
	if err != nil {
		return out, err
	}
	if action == "create" {
		out.ProviderTemplateID = smsStringValue(res, "TemplateCode")
	}
	if action == "query" {
		out.Status = smsAudit(smsStringValue(res, "TemplateStatus"), 0)
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

// call 调用阿里云短信 API（ACS3 签名）。
func (p *aliyunSMSAdapter) call(ctx context.Context, action string, params map[string]string) (map[string]any, error) {
	if p.settings["sms_access_key"] == "" || p.settings["sms_secret_key"] == "" {
		return nil, errors.New("阿里云短信密钥未配置")
	}
	host := "dysmsapi.aliyuncs.com"
	endpoint := "https://" + host + "/"
	headers := map[string]string{
		"host":                     host,
		"x-acs-action":             action,
		"x-acs-version":            "2017-05-25",
		"x-acs-content-sha256":     sha256Hex(nil),
		"x-acs-date":               time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"x-acs-signature-nonce":    randomHex(16),
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonicalHeaders strings.Builder
	var signed strings.Builder
	for i, k := range keys {
		canonicalHeaders.WriteString(k + ":" + headers[k] + "\n")
		signed.WriteString(k)
		if i < len(keys)-1 {
			signed.WriteByte(';')
		}
	}
	query := canonicalQuery(params)
	canonical := "POST\n/\n" + query + "\n" + canonicalHeaders.String() + "\n" + signed.String() + "\n" + sha256Hex(nil)
	signature := hexEncode(hmacSHA256([]byte(p.settings["sms_secret_key"]), []byte("ACS3-HMAC-SHA256\n"+sha256Hex([]byte(canonical)))))
	headers["Authorization"] = "ACS3-HMAC-SHA256 Credential=" + p.settings["sms_access_key"] + ",SignedHeaders=" + signed.String() + ",Signature=" + signature
	raw, err := smsDoRequest(ctx, p.client, http.MethodPost, endpoint+"?"+query, nil, headers)
	if err != nil {
		return nil, err
	}
	res, err := decodeSMSResponse(raw)
	if err != nil {
		return nil, err
	}
	if smsStringValue(res, "Code") == "" {
		return nil, smsUnknownError{}
	}
	if smsStringValue(res, "Code") != "OK" {
		_, e := smsRejected()
		return nil, e
	}
	return res, nil
}

func templateParamsJSON(values map[string]string) []byte {
	if values == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(values)
	return b
}

func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

var descriptorAliyunSMS = ProviderDescriptor{
	Key: "aliyun_sms", Name: "阿里云短信",
	ConfigFields: []string{"sms_access_key", "sms_secret_key", "sms_sign_name", "sms_endpoint"},
	Capabilities: SMSProviderCapabilities{Ranges: []SMSRange{SMSRangeCN, SMSRangeGlobal}, OTP: true, Notification: true, TemplateCRUD: true, AuditSync: true},
}

func init() {
	smsdk.RegisterSMSProvider(descriptorAliyunSMS, newAliyunSMSAdapter)
}
