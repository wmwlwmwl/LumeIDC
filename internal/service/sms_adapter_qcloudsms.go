package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// qcloudsmsAdapter 腾讯云短信适配器。
type qcloudsmsAdapter struct {
	settings map[string]string
	client   *http.Client
}

func newQcloudSMSAdapter(settings map[string]string, client *http.Client) (SMSProvider, error) {
	return &qcloudsmsAdapter{settings: settings, client: client}, nil
}

func (p *qcloudsmsAdapter) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor("qcloudsms")
	return d
}

func (p *qcloudsmsAdapter) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	out := SMSProviderResult{}
	if !smsProviderSupports("qcloudsms", t.Range, t.Kind) {
		return out, errors.New("短信服务商不支持该范围或类型")
	}
	var err error
	phone, err = smsPhoneForRange(phone, t.Range)
	if err != nil {
		return out, err
	}
	if t.ID == "" {
		return out, errors.New("腾讯云模板编码未配置")
	}
	signName := t.SignName
	if signName == "" {
		signName = p.settings["sms_sign_name"]
	}
	params := map[string]any{"PhoneNumberSet": []string{phone}, "SmsSdkAppId": p.settings["sms_username"], "TemplateId": t.ID, "TemplateParamSet": qcloudTemplateParams(values)}
	if t.Range != SMSRangeGlobal {
		params["SignName"] = strings.Trim(signName, "【】")
	}
	res, err := p.tencent(ctx, "SendSms", params)
	if err != nil {
		return out, err
	}
	statuses, _ := res["SendStatusSet"].([]any)
	if len(statuses) != 1 {
		return out, smsUnknownError{}
	}
	s, _ := statuses[0].(map[string]any)
	if smsStringValue(s, "Code") == "" {
		return out, smsUnknownError{}
	}
	if smsStringValue(s, "Code") != "Ok" {
		return smsRejected()
	}
	return SMSProviderResult{ProviderMessageID: smsStringValue(s, "SerialNo"), RequestID: smsStringValue(res, "RequestId")}, nil
}

func (p *qcloudsmsAdapter) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "create", t)
}

func (p *qcloudsmsAdapter) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.templateOp(ctx, "update", t)
}

func (p *qcloudsmsAdapter) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "query", SMSProviderTemplate{ID: id, Range: r})
}

func (p *qcloudsmsAdapter) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.templateOp(ctx, "delete", SMSProviderTemplate{ID: id, Range: r})
}

func (p *qcloudsmsAdapter) templateOp(ctx context.Context, action string, t SMSProviderTemplate) (SMSProviderResult, error) {
	out := SMSProviderResult{ProviderTemplateID: t.ID, Status: SMSAuditPending}
	if !p.Descriptor().Capabilities.TemplateCRUD {
		return smsProviderAuditNotSupported("qcloudsms"), errors.New("该服务商不支持此范围的远程模板操作，请在供应商控制台管理")
	}
	kind := t.Kind
	if kind == "" {
		kind = "notification"
	}
	if !smsProviderSupports("qcloudsms", t.Range, kind) {
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

	actions := map[string]string{"create": "AddSmsTemplate", "update": "ModifySmsTemplate", "query": "DescribeSmsTemplateList", "delete": "DeleteSmsTemplate"}
	params := map[string]any{}
	international := 0
	if t.Range == SMSRangeGlobal {
		international = 1
	}
	if action != "create" {
		id, err := strconv.ParseUint(t.ID, 10, 64)
		if err != nil || id == 0 {
			return out, errors.New("腾讯云模板编码须为正整数")
		}
		if action == "query" {
			params["TemplateIdSet"] = []uint64{id}
			params["International"] = international
		} else {
			params["TemplateId"] = id
		}
	}
	if action == "create" || action == "update" {
		typ := 0
		if t.Range == SMSRangeMarketing {
			typ = 1
		}
		params["SmsType"] = typ
		params["International"] = international
		params["TemplateName"] = t.Name
		params["TemplateContent"] = t.Content
		params["Remark"] = t.Remark
	}
	res, err := p.tencent(ctx, actions[action], params)
	if err != nil {
		return out, err
	}
	if action == "create" {
		s, _ := res["AddTemplateStatus"].(map[string]any)
		out.ProviderTemplateID = smsStringValue(s, "TemplateId")
	}
	if action == "query" {
		list, _ := res["DescribeTemplateStatusSet"].([]any)
		out.Status = SMSAuditUnknown
		for _, v := range list {
			s, _ := v.(map[string]any)
			if smsStringValue(s, "TemplateId") == t.ID {
				switch smsStringValue(s, "StatusCode") {
				case "0":
					out.Status = SMSAuditApproved
				case "1", "2":
					out.Status = SMSAuditPending
				case "-1":
					out.Status = SMSAuditRejected
				}
			}
		}
	}
	if action == "delete" {
		s, ok := res["DeleteTemplateStatus"].(map[string]any)
		if !ok || smsStringValue(s, "DeleteStatus") != "Delete Success" {
			return out, smsUnknownError{}
		}
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

func (p *qcloudsmsAdapter) tencent(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	if p.settings["sms_access_key"] == "" || p.settings["sms_secret_key"] == "" {
		return nil, errors.New("腾讯云短信密钥未配置")
	}
	endpoint, err := smsHTTPSURL(p.settings["sms_endpoint"], "https://sms.tencentcloudapi.com/")
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(params)
	now := time.Now()
	region := p.settings["sms_region"]
	if region == "" {
		region = "ap-guangzhou"
	}
	headers := map[string]string{
		"Content-Type":  "application/json; charset=utf-8",
		"X-TC-Action":   action,
		"X-TC-Version":  "2021-01-11",
		"X-TC-Region":   region,
		"X-TC-Timestamp": strconv.FormatInt(now.Unix(), 10),
		"Authorization": tc3Signature(p.settings["sms_access_key"], p.settings["sms_secret_key"], "sms.tencentcloudapi.com", action, body, now),
	}
	raw, err := smsDoRequest(ctx, p.client, http.MethodPost, endpoint, body, headers)
	if err != nil {
		return nil, err
	}
	res, err := decodeSMSResponse(raw)
	if err != nil {
		return nil, err
	}
	inner, ok := res["Response"].(map[string]any)
	if !ok {
		return nil, smsUnknownError{}
	}
	if _, ok := inner["Error"]; ok {
		_, e := smsRejected()
		return nil, e
	}
	return inner, nil
}

func init() {
	registerSMSProvider("qcloudsms", newQcloudSMSAdapter)
}
