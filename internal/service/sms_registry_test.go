package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	// 适配器已迁 plugins/sms（接口类扩展轨道），测试须显式聚合注册。
	_ "lumeidc/internal/plugins/sms"
)

// 真实 HTTP 服务接收请求，传输层只替换拨号目的地；生产请求仍必须使用供应商固定域名。
func smsMockClient(t *testing.T, fn http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: smsTestTransport(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		u := *req.URL
		copy.URL = &u
		copy.URL.Scheme = target.Scheme
		copy.URL.Host = target.Host
		copy.Host = req.URL.Host
		return http.DefaultTransport.RoundTrip(copy)
	})}
}
func TestSMSRegistryProtocols(t *testing.T) {
	for _, provider := range []string{"aliyun_sms", "qcloudsms", "submail", "smsbao", "idcsmart", "idcsmartpro", "stay33"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			settings := map[string]string{"sms_access_key": "测试应用", "sms_secret_key": "password", "sms_username": "测试账号", "sms_sign_name": "站点"}
			client := smsMockClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				switch provider {
				case "aliyun_sms":
					if r.Host != "dysmsapi.aliyuncs.com" || r.Header.Get("X-Acs-Action") != "SendSms" || !strings.HasPrefix(r.Header.Get("Authorization"), "ACS3-HMAC-SHA256 ") {
						t.Error("阿里请求签名头错误")
					}
					fmt.Fprint(w, `{"Code":"OK","extra":"允许新增响应字段"}`)
				case "qcloudsms":
					raw, _ := io.ReadAll(r.Body)
					var payload map[string]any
					if json.Unmarshal(raw, &payload) != nil {
						t.Error("腾讯JSON错误")
					}
					params, _ := payload["TemplateParamSet"].([]any)
					if len(params) != 12 || params[1] != "2" || params[9] != "10" {
						t.Error("腾讯参数顺序错误")
					}
					seconds, e := strconv.ParseInt(r.Header.Get("X-TC-Timestamp"), 10, 64)
					if e != nil || seconds < 1000000000 || r.Header.Get("Authorization") != tc3Signature("测试应用", "password", r.Host, "SendSms", raw, time.Unix(seconds, 0)) {
						t.Error("腾讯秒级时间戳或签名不一致")
					}
					fmt.Fprint(w, `{"Response":{"SendStatusSet":[{"Code":"Ok","SerialNo":"流水","Fee":1,"PhoneNumber":"已脱敏"}],"RequestId":"请求"}}`)
				case "smsbao":
					if r.Method != "GET" || r.Host != "api.smsbao.com" || r.URL.Path != "/sms" || r.Form.Get("p") != "5f4dcc3b5aa765d61d8327deb882cf99" || r.Form.Get("c") != "【站点】中文 & + 内容" || r.Form.Get("m") != "13800138000" {
						t.Error("短信宝协议或编码错误")
					}
					fmt.Fprint(w, "0")
				case "submail":
					if r.Host != "api.mysubmail.com" || r.URL.Path != "/message/send.json" || r.Form.Get("appid") != "测试应用" || r.Form.Get("signature") != "password" || r.Form.Get("content") != "【站点】中文 & + 内容" {
						t.Error("赛邮协议错误")
					}
					fmt.Fprint(w, `{"status":"success","send_id":"发送编号"}`)
				case "idcsmart", "idcsmartpro":
					path := "/smsapi.php"
					if provider == "idcsmartpro" {
						path = "/smsproapi.php"
					}
					if r.Host != "api1.idcsmart.com" || r.URL.Path != path || r.URL.Query().Get("action") != "send" || r.Header.Get("Api") != "测试账号" || r.Header.Get("Key") != "password" || r.Form.Get("vars[code]") != "123456" {
						t.Error("智简接口路径、头或变量错误")
					}
					fmt.Fprint(w, `{"status":200,"msg":"成功"}`)
				case "stay33":
					if r.Form.Get("content") != "中文 & + 内容" {
						t.Error("兼容正文被二次替换")
					}
					fmt.Fprint(w, `{"code":1}`)
				}
			})
			p, e := NewSMSProvider(provider, settings, client)
			if e != nil {
				t.Fatal(e)
			}
			scope := SMSRangeCN
			if provider == "idcsmartpro" {
				scope = SMSRangeMarketing
			}
			values := map[string]string{"code": "123456"}
			if provider == "qcloudsms" {
				values = map[string]string{}
				for i := 1; i <= 12; i++ {
					values["param"+strconv.Itoa(i)] = strconv.Itoa(i)
				}
			}
			_, e = p.SendSMS(context.Background(), "+8613800138000", SMSProviderTemplate{ID: "123", Kind: "notification", Range: scope, Content: "中文 & + 内容"}, values)
			if e != nil || calls != 1 {
				t.Fatalf("发送协议失败，调用%d次：%v", calls, e)
			}
		})
	}
}
func TestSMSGlobalCredentialsAndRange(t *testing.T) {
	calls := 0
	p, _ := NewSMSProvider("submail", map[string]string{"sms_access_key": "国内", "sms_secret_key": "国内密钥", "sms_global_access_key": "国际", "sms_global_secret_key": "国际密钥", "sms_global_sign_name": "国际签名"}, smsMockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		if r.URL.Path != "/internationalsms/send.json" || r.Form.Get("appid") != "国际" || r.Form.Get("signature") != "国际密钥" || r.Form.Get("content") != "【国际签名】内容" {
			t.Error("国际凭据或范围错误")
		}
		fmt.Fprint(w, `{"status":"success"}`)
	}))
	template := SMSProviderTemplate{Kind: "notification", Range: SMSRangeGlobal, Content: "内容"}
	if _, e := p.SendSMS(context.Background(), "+14155552671", template, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := p.SendSMS(context.Background(), "+8613800138000", template, nil); e == nil || calls != 1 {
		t.Fatal("国际模板错误发送到国内号码")
	}
	if _, e := p.TemplateQuery(context.Background(), "123", SMSRangeGlobal); e == nil || calls != 1 {
		t.Fatal("赛邮国际模板能力虚报成功")
	}
	for _, phone := range []string{"+14155552671", "+86123456789", "+8613800138000,13800138001"} {
		if _, e := smsPhoneForRange(phone, SMSRangeCN); e == nil {
			t.Fatal("国内号码范围校验失效")
		}
	}
	if smsProviderSupports("idcsmartpro", SMSRangeMarketing, "otp") || smsProviderSupports("idcsmartpro", SMSRangeCN, "notification") {
		t.Fatal("营销通道接受验证码或国内范围")
	}
}
func TestSMSRemoteProtocolsAndAudit(t *testing.T) {
	for _, provider := range []string{"aliyun_sms", "qcloudsms", "submail", "idcsmart", "idcsmartpro"} {
		t.Run(provider, func(t *testing.T) {
			operation := "create"
			calls := 0
			scope := SMSRangeCN
			if provider == "idcsmartpro" {
				scope = SMSRangeMarketing
			}
			p, _ := NewSMSProvider(provider, map[string]string{"sms_access_key": "应用", "sms_secret_key": "密钥", "sms_username": "账号", "sms_sign_name": "签名"}, smsMockClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				switch provider {
				case "aliyun_sms":
					if r.Header.Get("X-Acs-Action") != map[string]string{"create": "AddSmsTemplate", "update": "ModifySmsTemplate", "query": "QuerySmsTemplate", "delete": "DeleteSmsTemplate"}[operation] {
						t.Error("阿里模板动作错误")
					}
					fmt.Fprint(w, `{"Code":"OK","TemplateCode":"123","TemplateStatus":1}`)
				case "qcloudsms":
					var body map[string]any
					json.Unmarshal(raw, &body)
					if operation == "query" {
						if _, ok := body["TemplateIdSet"].([]any); !ok {
							t.Error("模板查询参数错误")
						}
						fmt.Fprint(w, `{"Response":{"DescribeTemplateStatusSet":[{"TemplateId":123,"StatusCode":2,"ReviewReply":"通过待生效"}]}}`)
					} else {
						fmt.Fprint(w, `{"Response":{"AddTemplateStatus":{"TemplateId":123},"ModifyTemplateStatus":{"TemplateId":123},"DeleteTemplateStatus":{"DeleteStatus":"Delete Success","DeleteTime":1}}}`)
					}
				default:
					if r.Method != map[string]string{"create": "POST", "update": "PUT", "query": "GET", "delete": "DELETE"}[operation] {
						t.Error("模板HTTP方法错误")
					}
					f, _ := url.ParseQuery(string(raw))
					if operation == "create" || operation == "update" {
						key := "content"
						if provider == "submail" {
							key = "sms_content"
						}
						if f.Get(key) != "验证码@var(code)" {
							t.Error("模板正文丢失")
						}
					}
					if provider == "submail" {
						fmt.Fprint(w, `{"status":"success","template_id":"123","template":{"template_id":"123","template_status":3}}`)
					} else {
						fmt.Fprint(w, `{"status":200,"template_id":"123","template":{"template_id":"123","status":2}}`)
					}
				}
			}))
			template := SMSProviderTemplate{Name: "验证码", Content: "验证码@var(code)", Kind: "notification", Range: scope}
			created, e := p.TemplateCreate(context.Background(), template)
			if e != nil || created.ProviderTemplateID != "123" || created.Status != SMSAuditPending {
				t.Fatal("创建不能伪审核", created, e)
			}
			operation = "update"
			template.ID = "123"
			updated, e := p.TemplateUpdate(context.Background(), template)
			if e != nil || updated.Status != SMSAuditPending {
				t.Fatal("更新审核状态错误", e)
			}
			operation = "query"
			queried, e := p.TemplateQuery(context.Background(), "123", scope)
			want := SMSAuditApproved
			if provider == "qcloudsms" {
				want = SMSAuditPending
			}
			if provider == "submail" {
				want = SMSAuditRejected
			}
			if e != nil || queried.Status != want {
				t.Fatal("审核映射错误", queried, e)
			}
			operation = "delete"
			deleted, e := p.TemplateDelete(context.Background(), "123", scope)
			if e != nil || deleted.ProviderTemplateID != "" || calls != 4 {
				t.Fatal("远程删除失败", e)
			}
		})
	}
	for _, provider := range []string{"smsbao", "stay33", "aliyun"} {
		p, _ := NewSMSProvider(provider, nil, nil)
		r, e := p.TemplateCreate(context.Background(), SMSProviderTemplate{})
		if e == nil || r.Status != SMSAuditNotSupported {
			t.Fatal("不支持审核不能伪成功")
		}
	}
}
func TestSMSUnknownNoRetryAndEndpoint(t *testing.T) {
	for _, body := range []string{"{}", "不是JSON", strings.Repeat("字", 400000)} {
		calls := 0
		p, _ := NewSMSProvider("submail", map[string]string{"sms_access_key": "应用", "sms_secret_key": "密钥"}, smsMockClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, body) }))
		_, e := p.SendSMS(context.Background(), "+8613800138000", SMSProviderTemplate{Kind: "notification", Range: SMSRangeCN, Content: "内容"}, nil)
		var unknown smsUnknownError
		if !errors.As(e, &unknown) || calls != 1 {
			t.Fatal("不确定响应未按未知且单次处理", e)
		}
	}
	for _, endpoint := range []string{"http://api.smsbao.com/sms", "https://evil.example/sms", "https://api.smsbao.com:444/sms", "https://user@api.smsbao.com/sms", "https://api.smsbao.com/sms?leak=1"} {
		if _, e := smsHTTPSURL(endpoint, "https://api.smsbao.com/sms"); e == nil {
			t.Fatal("不可信接口被接受")
		}
	}
	calls := 0
	p, _ := NewSMSProvider("smsbao", map[string]string{"sms_username": "账号", "sms_secret_key": "密码"}, smsMockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "https://evil.example/")
		w.WriteHeader(302)
	}))
	_, e := p.SendSMS(context.Background(), "+8613800138000", SMSProviderTemplate{Kind: "notification", Content: "内容"}, nil)
	if e == nil || calls != 1 {
		t.Fatal("重定向或重试失控")
	}
}
func TestSMSAuditAndCloudRendering(t *testing.T) {
	for _, status := range []SMSAuditStatus{SMSAuditPending, SMSAuditRejected} {
		if smsTemplateSendable(SMSTemplate{Provider: "submail", AuditStatus: status}) == nil {
			t.Fatal("待审核模板可发送")
		}
	}
	if smsTemplateSendable(SMSTemplate{Provider: "aliyun_sms", TemplateCode: "SMS_MANUAL", AuditStatus: SMSAuditUnknown}) != nil {
		t.Fatal("旧手填编码被破坏")
	}
	scene, _ := smsScene("otp_login")
	for _, provider := range []string{"aliyun_sms", "qcloudsms"} {
		param := "code"
		want := "验证码${code}"
		if provider == "qcloudsms" {
			param = "param1"
			want = "验证码{1}"
		}
		template := SMSTemplate{Name: "验证码", Kind: "otp", Provider: provider, Content: "验证码{{code}}", Parameters: map[string]string{param: "code"}}
		preview, e := renderSMSTemplate(template, scene, smsExamples(scene))
		if e != nil || preview.Content != "验证码123456" || smsRemoteTemplate(template).Content != want {
			t.Fatal("云模板参数转换失败", e)
		}
	}
}
func TestSMSDatabaseCapabilities(t *testing.T) {
	d := mailTestDB(t)
	n, _ := mailTestNotifier(t, d)
	ctx := context.Background()
	if e := n.SaveSMSSettings(ctx, map[string]string{"sms_provider": "submail", "sms_access_key": "国内应用", "sms_secret_key": "国内密钥", "sms_global_access_key": "国际应用", "sms_global_secret_key": "国际密钥"}); e != nil {
		t.Fatal(e)
	}
	if e := n.SaveSMSSettings(ctx, map[string]string{"sms_provider": "submail", "sms_access_key": "国内应用", "sms_secret_key": "", "sms_global_secret_key": ""}); e != nil {
		t.Fatal(e)
	}
	secret, _ := n.Settings.Get(ctx, "sms_global_secret_key")
	if secret != "国际密钥" {
		t.Fatal("同供应商空密钥未保留")
	}
	if e := n.SaveSMSSettings(ctx, map[string]string{"sms_provider": "smsbao", "sms_username": "新账号", "sms_secret_key": "新密码"}); e != nil {
		t.Fatal(e)
	}
	secret, _ = n.Settings.Get(ctx, "sms_global_secret_key")
	if secret != "" {
		t.Fatal("跨供应商继承国际密钥")
	}
	if e := n.SaveSMSBinding(ctx, SMSBinding{Code: "otp_login", Enabled: true}); e != nil {
		t.Fatal("065验证码解绑约束未修复", e)
	}
	id, e := n.SaveSMSTemplate(ctx, SMSTemplate{Name: "验证码", Kind: "otp", Provider: "smsbao", Content: "{{code}}", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	mailExec(t, d, `UPDATE sms_templates SET remote_operation='create' WHERE id=$1`, id)
	if e := n.DeleteSMSTemplate(ctx, id); e == nil {
		t.Fatal("删除了操作未确认模板")
	}
	if e := n.SaveSMSSettings(ctx, map[string]string{"sms_provider": "stay33", "sms_secret_key": "密码"}); e == nil {
		t.Fatal("远程操作中切换了凭据")
	}
}
