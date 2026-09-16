package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type configuredSMSProvider struct {
	key      string
	settings map[string]string
	client   *http.Client
}

func NewSMSProvider(key string, settings map[string]string, client *http.Client) (SMSProvider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if _, ok := SMSProviderDescriptorFor(key); !ok {
		return nil, errors.New("短信服务商不受支持")
	}
	safe := (&ConfiguredSMSProvider{Client: client}).client()
	return &configuredSMSProvider{key, settings, safe}, nil
}
func (p *configuredSMSProvider) Descriptor() ProviderDescriptor {
	d, _ := SMSProviderDescriptorFor(p.key)
	return d
}
func (p *configuredSMSProvider) request(ctx context.Context, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("短信请求无效")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := p.client.Do(req)
	if e != nil {
		return nil, smsUnknownError{}
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, smsUnknownError{}
	}
	return bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), nil
}
func decodeSMSResponse(raw []byte) (map[string]any, error) {
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&out) != nil || out == nil || d.Decode(new(any)) != io.EOF {
		return nil, smsUnknownError{}
	}
	return out, nil
}
func (p *configuredSMSProvider) form(ctx context.Context, method, endpoint string, form url.Values, headers map[string]string) (map[string]any, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	var body []byte
	if method == http.MethodGet {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint += sep + form.Encode()
	} else {
		body = []byte(form.Encode())
	}
	raw, e := p.request(ctx, method, endpoint, body, headers)
	if e != nil {
		return nil, e
	}
	return decodeSMSResponse(raw)
}
func smsRejected() (SMSProviderResult, error) {
	return SMSProviderResult{ProviderCode: "provider_rejected", Message: "短信供应商明确拒绝请求"}, errors.New("短信供应商明确拒绝请求，请检查供应商控制台")
}
func (p *configuredSMSProvider) SendSMS(ctx context.Context, phone string, t SMSProviderTemplate, values map[string]string) (SMSProviderResult, error) {
	if !smsProviderSupports(p.key, t.Range, t.Kind) {
		return SMSProviderResult{}, errors.New("短信服务商不支持该范围或类型")
	}
	phone, e := smsPhoneForRange(phone, t.Range)
	if e != nil {
		return SMSProviderResult{}, e
	}
	if t.SignName == "" {
		t.SignName = p.settings["sms_sign_name"]
		if p.key == "submail" && t.Range == SMSRangeGlobal {
			t.SignName = p.settings["sms_global_sign_name"]
		}
	}
	legacy := &ConfiguredSMSProvider{Client: p.client}
	get := func(k string) string {
		switch k {
		case "sms_template_code", "sms_bound_template_code":
			return t.ID
		case "sms_sign_name":
			return t.SignName
		case "sms_rendered_content":
			return t.Content
		case "sms_rendered_parameters":
			b, _ := json.Marshal(values)
			return string(b)
		}
		return p.settings[k]
	}
	switch p.key {
	case "aliyun":
		e = legacy.sendAliyunPNVS(ctx, phone, values["code"], "", get)
	case "aliyun_sms":
		e = legacy.sendAliyunSMS(ctx, phone, "", "", get)
	case "stay33":
		e = legacy.sendStay33(ctx, phone, "", "", get)
	case "smsbao":
		path := "/sms"
		if t.Range == SMSRangeGlobal {
			path = "/wsms"
		} else {
			phone = strings.TrimPrefix(phone, "+86")
		}
		endpoint, err := smsHTTPSURL(p.settings["sms_endpoint"], "https://api.smsbao.com"+path)
		if err != nil {
			return SMSProviderResult{}, err
		}
		if p.settings["sms_username"] == "" || p.settings["sms_secret_key"] == "" {
			return SMSProviderResult{}, errors.New("短信宝账号或密码未配置")
		}
		form := url.Values{"u": {p.settings["sms_username"]}, "p": {md5Hex(p.settings["sms_secret_key"])}, "m": {phone}, "c": {smsSign(t.SignName, t.Content)}}
		raw, err := p.request(ctx, http.MethodGet, endpoint+"?"+form.Encode(), nil, nil)
		if err != nil {
			return SMSProviderResult{}, err
		}
		code := strings.TrimSpace(string(raw))
		if code != "0" {
			switch code {
			case "30", "40", "41", "42", "43", "50", "51", "-1", "-2", "18446744073709551615", "18446744073709551614":
				return smsRejected()
			default:
				return SMSProviderResult{}, smsUnknownError{}
			}
		}
	case "submail", "idcsmart", "idcsmartpro":
		form := url.Values{"to": {phone}, "content": {smsSign(t.SignName, t.Content)}}
		if t.Range != SMSRangeGlobal {
			form.Set("to", strings.TrimPrefix(phone, "+86"))
		}
		if p.key != "submail" {
			form.Set("template_id", t.ID)
			for k, v := range values {
				form.Set("vars["+k+"]", v)
			}
		}
		_, e = p.textAPI(ctx, "send", http.MethodPost, t.Range, form)
	case "qcloudsms":
		if t.ID == "" {
			return SMSProviderResult{}, errors.New("腾讯云模板编码未配置")
		}
		params := map[string]any{"PhoneNumberSet": []string{phone}, "SmsSdkAppId": p.settings["sms_username"], "TemplateId": t.ID, "TemplateParamSet": qcloudTemplateParams(values)}
		if t.Range != SMSRangeGlobal {
			params["SignName"] = strings.Trim(t.SignName, "【】")
		}
		res, err := p.tencent(ctx, "SendSms", params)
		if err != nil {
			return SMSProviderResult{}, err
		}
		statuses, _ := res["SendStatusSet"].([]any)
		if len(statuses) != 1 {
			return SMSProviderResult{}, smsUnknownError{}
		}
		s, _ := statuses[0].(map[string]any)
		if smsStringValue(s, "Code") == "" {
			return SMSProviderResult{}, smsUnknownError{}
		}
		if smsStringValue(s, "Code") != "Ok" {
			return smsRejected()
		}
		return SMSProviderResult{ProviderMessageID: smsStringValue(s, "SerialNo"), RequestID: smsStringValue(res, "RequestId")}, nil
	}
	return SMSProviderResult{}, e
}
func (p *configuredSMSProvider) textAPI(ctx context.Context, action, method string, r SMSRange, form url.Values) (map[string]any, error) {
	var endpoint string
	headers := map[string]string{}
	if p.key == "submail" {
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
		endpoint = "https://api.mysubmail.com/" + path
		form.Set("appid", p.settings[prefix+"access_key"])
		form.Set("signature", p.settings[prefix+"secret_key"])
		form.Set("appkey", p.settings[prefix+"secret_key"])
		form.Set("timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	} else {
		if p.settings["sms_username"] == "" || p.settings["sms_secret_key"] == "" {
			return nil, errors.New("智简短信账号或密钥未配置")
		}
		path := "smsapi.php"
		if p.key == "idcsmartpro" {
			path = "smsproapi.php"
		}
		endpoint = "https://api1.idcsmart.com/" + path
		headers["api"] = p.settings["sms_username"]
		headers["key"] = p.settings["sms_secret_key"]
	}
	checked, e := smsHTTPSURL(p.settings["sms_endpoint"], endpoint)
	if e != nil {
		return nil, e
	}
	endpoint = checked
	if p.key != "submail" {
		endpoint += "?action=" + action
	}
	res, e := p.form(ctx, method, endpoint, form, headers)
	if e != nil {
		return nil, e
	}
	status := smsStringValue(res, "status")
	if status == "" {
		return nil, smsUnknownError{}
	}
	if (p.key == "submail" && status != "success") || (p.key != "submail" && status != "200") {
		_, e := smsRejected()
		return nil, e
	}
	return res, nil
}
func (p *configuredSMSProvider) tencent(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	if p.settings["sms_access_key"] == "" || p.settings["sms_secret_key"] == "" {
		return nil, errors.New("腾讯云短信密钥未配置")
	}
	endpoint, e := smsHTTPSURL(p.settings["sms_endpoint"], "https://sms.tencentcloudapi.com/")
	if e != nil {
		return nil, e
	}
	body, _ := json.Marshal(params)
	now := time.Now()
	region := p.settings["sms_region"]
	if region == "" {
		region = "ap-guangzhou"
	}
	headers := map[string]string{"Content-Type": "application/json; charset=utf-8", "X-TC-Action": action, "X-TC-Version": "2021-01-11", "X-TC-Region": region, "X-TC-Timestamp": strconv.FormatInt(now.Unix(), 10), "Authorization": tc3Signature(p.settings["sms_access_key"], p.settings["sms_secret_key"], "sms.tencentcloudapi.com", action, body, now)}
	raw, e := p.request(ctx, http.MethodPost, endpoint, body, headers)
	if e != nil {
		return nil, e
	}
	res, e := decodeSMSResponse(raw)
	if e != nil {
		return nil, e
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
func (p *configuredSMSProvider) aliyun(ctx context.Context, action string, params map[string]string) (map[string]any, error) {
	endpoint, e := smsHTTPSURL(p.settings["sms_endpoint"], "https://dysmsapi.aliyuncs.com/")
	if e != nil {
		return nil, e
	}
	if p.settings["sms_access_key"] == "" || p.settings["sms_secret_key"] == "" {
		return nil, errors.New("阿里云短信密钥未配置")
	}
	headers := map[string]string{"host": "dysmsapi.aliyuncs.com", "x-acs-action": action, "x-acs-version": "2017-05-25", "x-acs-content-sha256": sha256Hex(nil), "x-acs-date": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "x-acs-signature-nonce": randomHex(16)}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	query := canonicalQuery(params)
	signed := strings.Join(keys, ";")
	canonical := "POST\n/\n" + query + "\n" + canonicalHeaders.String() + "\n" + signed + "\n" + sha256Hex(nil)
	signature := hex.EncodeToString(hmacSHA256([]byte(p.settings["sms_secret_key"]), []byte("ACS3-HMAC-SHA256\n"+sha256Hex([]byte(canonical)))))
	headers["Authorization"] = "ACS3-HMAC-SHA256 Credential=" + p.settings["sms_access_key"] + ",SignedHeaders=" + signed + ",Signature=" + signature
	raw, e := p.request(ctx, http.MethodPost, endpoint+"?"+query, nil, headers)
	if e != nil {
		return nil, e
	}
	res, e := decodeSMSResponse(raw)
	if e != nil {
		return nil, e
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
func (p *configuredSMSProvider) TemplateCreate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.template(ctx, "create", t)
}
func (p *configuredSMSProvider) TemplateUpdate(ctx context.Context, t SMSProviderTemplate) (SMSProviderResult, error) {
	return p.template(ctx, "update", t)
}
func (p *configuredSMSProvider) TemplateQuery(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.template(ctx, "query", SMSProviderTemplate{ID: id, Range: r})
}
func (p *configuredSMSProvider) TemplateDelete(ctx context.Context, id string, r SMSRange) (SMSProviderResult, error) {
	return p.template(ctx, "delete", SMSProviderTemplate{ID: id, Range: r})
}
func (p *configuredSMSProvider) template(ctx context.Context, action string, t SMSProviderTemplate) (SMSProviderResult, error) {
	out := SMSProviderResult{ProviderTemplateID: t.ID, Status: SMSAuditPending}
	if !p.Descriptor().Capabilities.TemplateCRUD || (p.key == "submail" && t.Range == SMSRangeGlobal) {
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
	if t.SignName == "" {
		t.SignName = p.settings["sms_sign_name"]
	}
	var res map[string]any
	var e error
	switch p.key {
	case "aliyun_sms":
		actions := map[string]string{"create": "AddSmsTemplate", "update": "ModifySmsTemplate", "query": "QuerySmsTemplate", "delete": "DeleteSmsTemplate"}
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
		res, e = p.aliyun(ctx, actions[action], params)
		if e != nil {
			return out, e
		}
		if action == "create" {
			out.ProviderTemplateID = smsStringValue(res, "TemplateCode")
		}
		if action == "query" {
			out.Status = smsAudit(smsStringValue(res, "TemplateStatus"), 0)
		}
	case "qcloudsms":
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
		res, e = p.tencent(ctx, actions[action], params)
		if e != nil {
			return out, e
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
		}
	case "submail", "idcsmart", "idcsmartpro":
		form := url.Values{}
		if action != "create" {
			form.Set("template_id", t.ID)
		}
		if action == "create" || action == "update" {
			if p.key == "submail" {
				form.Set("sms_title", t.Name)
				form.Set("sms_signature", t.SignName)
				form.Set("sms_content", t.Content)
			} else {
				form.Set("title", t.Name)
				form.Set("signature", smsSign(t.SignName, ""))
				form.Set("content", t.Content)
			}
		}
		method := map[string]string{"create": "POST", "update": "PUT", "query": "GET", "delete": "DELETE"}[action]
		res, e = p.textAPI(ctx, "template", method, t.Range, form)
		if e != nil {
			return out, e
		}
		if action == "create" {
			out.ProviderTemplateID = smsStringValue(res, "template_id")
		}
		if action == "query" {
			s, _ := res["template"].(map[string]any)
			key := "status"
			if p.key == "submail" {
				key = "template_status"
			}
			out.Status = smsAudit(smsStringValue(s, key), 1)
		}
	}
	if action == "create" && !smsIdentifier.MatchString(out.ProviderTemplateID) {
		return out, smsUnknownError{}
	}
	if action == "delete" {
		out.Status = SMSAuditUnknown
		out.ProviderTemplateID = ""
	}
	out.Message = map[SMSAuditStatus]string{SMSAuditPending: "供应商审核中", SMSAuditApproved: "供应商审核通过", SMSAuditRejected: "供应商审核未通过，请查看供应商控制台", SMSAuditUnknown: "供应商审核状态未知"}[out.Status]
	return out, nil
}
