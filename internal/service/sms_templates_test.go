package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"lumeidc/internal/repo"
)

type smsTestTransport func(*http.Request) (*http.Response, error)

func (f smsTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func smsTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestSMSTemplateScenesAndValidation(t *testing.T) {
	scenes := defaultSMSScenes()
	if len(scenes) != 20 {
		t.Fatal("固定短信场景数量不正确")
	}
	for _, purpose := range []string{"register", "login", "reset_password", "bind", "change", "profile_phone_old", "verify_phone"} {
		scene, err := smsScene("otp_" + purpose)
		if err != nil || !scene.Required || !scene.Enabled {
			t.Fatal("验证码用途未注册")
		}
		template := SMSTemplate{Name: "验证码", Provider: "stay33", Kind: "otp", Content: "【{{sign_name}}】{{purpose}}：{{code}}", Enabled: true}
		preview, err := renderSMSTemplate(template, scene, smsExamples(scene))
		if err != nil || !strings.Contains(preview.Content, purpose) || !strings.Contains(preview.Content, "123456") {
			t.Fatal("验证码用途或验证码丢失", err)
		}
	}
	if _, err := smsScene("otp_verify_bound_phone"); err == nil {
		t.Fatal("注册了不存在的用途")
	}
	scene, _ := smsScene("ticket_reply")
	template := SMSTemplate{Name: "通知", Provider: "stay33", Kind: "notification", Content: "{{subject}}"}
	for _, content := range []string{"{{code}}", "{{unknown}}", "{{ subject }}", "{{subject", "{{{subject}}}", "换行\n{{subject}}", strings.Repeat("字", 501)} {
		template.Content = content
		if _, err := renderSMSTemplate(template, scene, smsExamples(scene)); err == nil {
			t.Fatal("非法模板被接受", content)
		}
	}
	template.Content = "{{subject}}"
	for _, v := range []map[string]string{{}, {"subject": ""}, {"subject": "控制\x00字符"}, {"subject": strings.Repeat("字", 501)}} {
		if _, err := renderSMSTemplate(template, scene, v); err == nil {
			t.Fatal("非法业务变量被接受")
		}
	}
	template.Provider = "aliyun"
	template.Content = ""
	template.TemplateCode = "100001"
	if _, err := renderSMSTemplate(template, scene, smsExamples(scene)); err == nil {
		t.Fatal("号码认证接受了通知")
	}
	otp, _ := smsScene("otp_login")
	template.Kind = "otp"
	if _, err := renderSMSTemplate(template, otp, smsExamples(otp)); err != nil {
		t.Fatal("号码认证隐式code映射失败", err)
	}
	template.Provider = "aliyun_sms"
	template.Parameters = map[string]string{"用途": "purpose"}
	if _, err := renderSMSTemplate(template, otp, smsExamples(otp)); err == nil {
		t.Fatal("非法参数名被接受")
	}
	template.Parameters = map[string]string{"purpose": "purpose"}
	if _, err := renderSMSTemplate(template, otp, smsExamples(otp)); err == nil {
		t.Fatal("验证码没有真正映射code")
	}
}

func TestSMSProviderJSONAndSingleAttempt(t *testing.T) {
	scene, _ := smsScene("ticket_reply")
	original := "主题\"反斜杠\\与<标签>"
	template := SMSTemplate{Name: "通知", Kind: "notification", Provider: "aliyun_sms", TemplateCode: "SMS_TEST", Parameters: map[string]string{"provider_subject": "subject"}}
	preview, err := renderSMSTemplate(template, scene, map[string]string{"subject": original})
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"sms_provider": "aliyun_sms", "sms_access_key": "测试标识", "sms_secret_key": "测试密钥", "sms_sign_name": "测试签名"}
	calls := 0
	p := &ConfiguredSMSProvider{Client: &http.Client{Transport: smsTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var parameters map[string]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("TemplateParam")), &parameters); err != nil || parameters["provider_subject"] != original {
			t.Fatal("参数JSON转义错误")
		}
		return smsTestResponse(`{"Code":"OK"}`), nil
	})}}
	if err := p.sendRendered(context.Background(), "+8613800138000", preview, settings, false); err != nil || calls != 1 {
		t.Fatal("普通短信请求失败", err)
	}
	for _, response := range []string{"断网", "{}", `{"Code":"Rejected"}`, `{"Code":"OK"}`} {
		calls = 0
		p.Client.Transport = smsTestTransport(func(*http.Request) (*http.Response, error) {
			calls++
			if response == "断网" {
				return nil, errors.New("模拟网络错误含隐私")
			}
			return smsTestResponse(response), nil
		})
		result, err := p.sendRenderedResult(context.Background(), "+8613800138000", preview, settings, false)
		delivery := smsResult(result, err)
		want := map[string]string{"断网": "unknown", "{}": "unknown", `{"Code":"Rejected"}`: "failed", `{"Code":"OK"}`: "sent"}[response]
		if delivery.status != want || calls != 1 || strings.Contains(delivery.message, "隐私") {
			t.Fatal("发送结果分类或尝试次数错误")
		}
	}
	calls = 0
	p.Client.Transport = smsTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		r := smsTestResponse("")
		r.StatusCode = 302
		r.Header.Set("Location", "https://不可信.example/")
		return r, nil
	})
	if err := p.sendRendered(context.Background(), "+8613800138000", preview, settings, false); err == nil || calls != 1 {
		t.Fatal("跟随了重定向")
	}
	settings["sms_provider"] = "stay33"
	calls = 0
	if err := p.sendRendered(context.Background(), "+8613800138000", preview, settings, false); err == nil || calls != 0 {
		t.Fatal("服务商不匹配仍发送")
	}
}

type purposeRecorder struct{ purpose, code string }

func (p *purposeRecorder) SendPurpose(_ context.Context, phone, code, purpose string) error {
	p.purpose = purpose
	p.code = code
	return nil
}
func TestSMSProviderResultMetadata(t *testing.T) {
	settings := map[string]string{"sms_provider": "qcloudsms", "sms_access_key": "secret-id", "sms_secret_key": "secret-key", "sms_username": "app-id", "sms_sign_name": "测试签名"}
	p := &ConfiguredSMSProvider{Client: &http.Client{Transport: smsTestTransport(func(*http.Request) (*http.Response, error) {
		return smsTestResponse(`{"Response":{"RequestId":"req-001","SendStatusSet":[{"Code":"Ok","SerialNo":"serial-001"}]}}`), nil
	})}}
	preview := SMSPreview{Provider: "qcloudsms", TemplateCode: "123", Parameters: map[string]string{"1": "值"}, RangeType: SMSRangeCN, SignName: "测试签名"}
	result, err := p.sendRenderedResult(context.Background(), "+8613800138000", preview, settings, false)
	if err != nil || result.RequestID != "req-001" || result.ProviderMessageID != "serial-001" {
		t.Fatal("未保留服务商请求或消息流水", err, result)
	}
	failed := smsResult(SMSProviderResult{ProviderCode: "provider_rejected", RequestID: "req-002"}, errors.New("拒绝"))
	if failed.status != "failed" || failed.errorCode != "provider_rejected" || failed.requestID != "req-002" {
		t.Fatal("明确拒绝未保留错误码或请求编号", failed)
	}
}

func TestSMSPNVSLocalCodeContract(t *testing.T) {
	scene, _ := smsScene("otp_reset_password")
	template := SMSTemplate{Name: "验证码", Kind: "otp", Provider: "aliyun", TemplateCode: "100009"}
	preview, err := renderSMSTemplate(template, scene, map[string]string{"code": "123456"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := &ConfiguredSMSProvider{Client: &http.Client{Transport: smsTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("Action") != "SendSmsVerifyCode" || r.Form.Get("TemplateCode") != "100009" || r.Form.Get("TemplateParam") != `{"code":"123456"}` {
			t.Fatal("号码认证自定义验证码协议被更改")
		}
		return smsTestResponse(`{"Code":"OK","Success":true}`), nil
	})}}
	settings := map[string]string{"sms_provider": "aliyun", "sms_access_key": "测试标识", "sms_secret_key": "测试密钥", "sms_sign_name": "测试站点"}
	if err := p.sendRendered(context.Background(), "+8613800138000", preview, settings, true); err != nil || calls != 1 {
		t.Fatal(err)
	}
	if err := p.sendRendered(context.Background(), "+8613800138000", preview, settings, false); err == nil || calls != 1 {
		t.Fatal("号码认证发送了普通通知")
	}
	auth := &AuthChallengeService{Key: []byte("测试密钥")}
	if auth.digest("code", "phone", "号码\x00login\x00123456") == auth.digest("code", "phone", "号码\x00register\x00123456") {
		t.Fatal("用途未参与本地摘要")
	}
}

func TestSMSBothOTPPurposePaths(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	_ = n
	ctx := context.Background()
	recorder := &purposeRecorder{}
	auth := &AuthChallengeService{Store: repo.NewAuthChallenges(d), SMS: recorder, Key: []byte("测试密钥")}
	for _, purpose := range []string{"register", "login", "reset_password", "profile_phone_old", "verify_phone"} {
		mailExec(t, d, `DELETE FROM auth_challenges`)
		if err := auth.Issue(ctx, "phone", purpose, "+8613800138000", "测试来源"); err != nil {
			t.Fatal(err)
		}
		if recorder.purpose != purpose {
			t.Fatal("匿名验证码用途丢失")
		}
		if err := auth.Verify(ctx, "phone", purpose, "+8613800138000", recorder.code); err != nil {
			t.Fatal("本地验证码HMAC契约被更改", err)
		}
	}
	identity := NewIdentity(repo.NewIdentityStore(d), repo.NewUsers(d), nil, nil, recorder, "测试密钥", nil, nil, "", nil)
	for _, purpose := range []string{"bind", "change"} {
		mailExec(t, d, `DELETE FROM phone_verification_challenges`)
		if purpose == "change" {
			mailExec(t, d, `UPDATE users SET phone_e164='+8613900138000' WHERE id=$1`, uid)
		}
		if err := identity.RequestPhoneCode(ctx, uid, "+8613800138000", purpose, "测试来源"); err != nil {
			t.Fatal(err)
		}
		if recorder.purpose != purpose {
			t.Fatal("身份验证码用途丢失")
		}
	}
}

func TestSMSTemplateBindingAndOutbox(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	ctx := context.Background()
	mailExec(t, d, `UPDATE users SET phone_e164='+8613800138000',phone_verified_at=now() WHERE id=$1`, uid)
	for key, value := range map[string]string{"sms_provider": "stay33", "sms_sign_name": "测试站点", "notify_email_forward_enabled": "0"} {
		if err := n.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.NotifyTemplate(ctx, uid, "ticket_reply", "标题", "正文", map[string]string{"subject": "主题"}); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "sms_outbox") != 0 {
		t.Fatal("未绑定默认通知被发送")
	}
	template := SMSTemplate{Name: "工单", Provider: "stay33", Kind: "notification", Content: "{{subject}}", Enabled: true}
	id, err := n.SaveSMSTemplate(ctx, template)
	if err != nil {
		t.Fatal(err)
	}
	template.ID = id
	binding := SMSBinding{Code: "ticket_reply", TemplateID: id, Enabled: true}
	if err := n.SaveSMSBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := n.DeleteSMSTemplate(ctx, id); err == nil {
		t.Fatal("删除了依赖模板")
	}
	template.Content = "{{amount}}"
	if _, err := n.SaveSMSTemplate(ctx, template); err == nil {
		t.Fatal("编辑模板破坏了已绑定场景")
	}
	template.Content = "{{subject}}"
	notify := func() {
		t.Helper()
		if err := n.NotifyTemplate(ctx, uid, "ticket_reply", "标题", "正文", map[string]string{"subject": "主题"}); err != nil {
			t.Fatal(err)
		}
	}
	notify()
	if mailCount(t, d, "sms_outbox") != 1 || mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("短信错误耦合邮件开关")
	}
	job, err := n.claimSMS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := n.smsJobAllowed(ctx, job); err != nil || !ok {
		t.Fatal("有效任务复核失败", err)
	}
	if err := n.Settings.Set(ctx, "sms_sign_name", "已更新签名"); err != nil {
		t.Fatal(err)
	}
	if result := n.deliverSMS(ctx, job); result.status != "skipped" || result.errorCode != "preflight_changed" {
		t.Fatal("路由配置指纹变化后仍发送")
	}
	if err := n.Settings.Set(ctx, "sms_sign_name", "测试站点"); err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `UPDATE users SET phone_e164='+8613900138000' WHERE id=$1`, uid)
	if result := n.deliverSMS(ctx, job); result.status != "skipped" {
		t.Fatal("换号后仍发送")
	}
	mailExec(t, d, `UPDATE users SET phone_e164='+8613800138000' WHERE id=$1`, uid)
	binding.Enabled = false
	if err := n.SaveSMSBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if result := n.deliverSMS(ctx, job); result.status != "skipped" {
		t.Fatal("关闭场景后仍发送")
	}
	binding.Enabled = true
	if err := n.SaveSMSBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `UPDATE users SET status=0 WHERE id=$1`, uid)
	if result := n.deliverSMS(ctx, job); result.status != "skipped" {
		t.Fatal("停用账户仍发送")
	}
	mailExec(t, d, `UPDATE users SET status=1 WHERE id=$1`, uid)
	mailExec(t, d, `UPDATE sms_outbox SET lease_until=now()-interval '1 second'`)
	if _, err := n.claimSMS(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("发送中断任务被重新领取")
	}
	if err := n.finishSMS(ctx, job, smsDeliveryResult{status: "sent", message: "发送成功", providerMessageID: "serial-1", requestID: "request-1"}); err != nil {
		t.Fatal(err)
	}
	records, err := n.ListSMSDeliveries(ctx)
	if err != nil || len(records) != 1 || records[0].Status != "unknown" || records[0].Recipient == job.recipient || records[0].ProviderMessageID != "" || records[0].RequestID != "" {
		t.Fatal("未知状态被旧发送者覆盖、流水写入错误或号码未脱敏", err)
	}
	mailExec(t, d, `UPDATE sms_outbox SET status='running',lease_until=now()+interval '1 minute' WHERE id=$1`, job.id)
	if err := n.finishSMS(ctx, job, smsDeliveryResult{status: "sent", message: "供应商已受理", providerMessageID: "serial-2", requestID: "request-2"}); err != nil {
		t.Fatal(err)
	}
	records, err = n.ListSMSDeliveries(ctx)
	if err != nil || records[0].Status != "sent" || records[0].ProviderMessageID != "serial-2" || records[0].RequestID != "request-2" || records[0].ErrorCode != "" {
		t.Fatal("投递流水未写入或读取", err, records)
	}
	notify()
	var notificationID int64
	if err := d.QueryRow(`SELECT notification_id FROM sms_outbox ORDER BY id DESC LIMIT 1`).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.enqueueSMS(ctx, tx, notificationID, uid, "ticket_reply", map[string]string{"subject": "主题"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "sms_outbox") != 2 {
		t.Fatal("同一通知重复入队")
	}
	otp := SMSTemplate{Name: "验证码", Provider: "stay33", Kind: "otp", Content: "{{code}}", Enabled: true}
	otp.ID, err = n.SaveSMSTemplate(ctx, otp)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SaveSMSBinding(ctx, SMSBinding{Code: "otp_login", TemplateID: otp.ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	otp.Enabled = false
	if _, err := n.SaveSMSTemplate(ctx, otp); err == nil {
		t.Fatal("依赖验证码模板被停用")
	}
	if err := n.SaveSMSBinding(ctx, SMSBinding{Code: "otp_login", TemplateID: otp.ID}); err == nil {
		t.Fatal("验证码场景被关闭")
	}
	if err := n.DeleteSMSTemplate(ctx, otp.ID); err == nil {
		t.Fatal("依赖验证码模板被删除")
	}
	if err := n.Settings.Set(ctx, "sms_provider", "aliyun_sms"); err != nil {
		t.Fatal(err)
	}
	if err := n.SendPurpose(ctx, "+8613800138000", "123456", "login"); err == nil {
		t.Fatal("绑定服务商不匹配静默降级")
	}
	if err := n.SaveSMSBinding(ctx, binding); err == nil {
		t.Fatal("绑定服务商不匹配被接受")
	}
	notify()
	records, err = n.ListSMSDeliveries(ctx)
	if err != nil || records[0].Status != "skipped" {
		t.Fatal("服务商切换后错误入队")
	}
	n.StartSMS()
	n.StartSMS()
	stopCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := n.StopSMS(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSameSMSParameters(t *testing.T) {
	if !sameSMSParameters(nil, map[string]string{}) {
		t.Fatal("nil 与空映射应视为相同")
	}
	if !sameSMSParameters(map[string]string{"code": "code"}, map[string]string{"code": "code"}) {
		t.Fatal("相同映射被判定为不同")
	}
	for _, pair := range [][2]map[string]string{
		{{"code": "code"}, {"code": "purpose"}},
		{{"code": "code"}, {"code": "code", "purpose": "purpose"}},
		{{"code": "code"}, {}},
	} {
		if sameSMSParameters(pair[0], pair[1]) {
			t.Fatal("不同映射被判定为相同")
		}
	}
}

func TestSMSTemplateLockAndAuditInvalidation(t *testing.T) {
	d := mailTestDB(t)
	n, _ := mailTestNotifier(t, d)
	ctx := context.Background()
	if err := n.Settings.Set(ctx, "sms_provider", "idcsmart"); err != nil {
		t.Fatal(err)
	}
	// 远程调用不参与本测试：直接写入审核结论与持久锁，验证本地边界。
	base := SMSTemplate{Name: "智简通知", Provider: "idcsmart", Kind: "notification", TemplateCode: "8899", Content: "{{subject}}", Enabled: true}
	id, err := n.SaveSMSTemplate(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `UPDATE sms_templates SET audit_status='approved',audit_message='供应商审核通过',audit_updated_at=now() WHERE id=$1`, id)
	// 本地正文变更后不得沿用旧的审核通过结论。
	base.ID, base.Content = id, "{{subject}}（已改）"
	if _, err := n.SaveSMSTemplate(ctx, base); err != nil {
		t.Fatal(err)
	}
	var status, message string
	if err := d.QueryRow(`SELECT audit_status,audit_message FROM sms_templates WHERE id=$1`, id).Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != string(SMSAuditUnknown) || !strings.Contains(message, "失效") {
		t.Fatal("本地正文修改后仍沿用旧审核结论", status, message)
	}
	// 审核、远程编号与远程操作字段都必须只读。
	for _, reject := range []SMSTemplate{
		{ID: id, Name: base.Name, Provider: base.Provider, Kind: base.Kind, TemplateCode: base.TemplateCode, Content: base.Content, Enabled: true, AuditStatus: SMSAuditApproved},
		{ID: id, Name: base.Name, Provider: base.Provider, Kind: base.Kind, TemplateCode: base.TemplateCode, Content: base.Content, Enabled: true, RemoteTemplateID: "9999"},
		{ID: id, Name: base.Name, Provider: base.Provider, Kind: base.Kind, TemplateCode: base.TemplateCode, Content: base.Content, Enabled: true, RemoteOperation: "create"},
	} {
		if _, err := n.SaveSMSTemplate(ctx, reject); err == nil {
			t.Fatal("只读字段被当作可写字段")
		}
	}
	// 持久锁必须阻止修改、删除、绑定与重复远程操作。
	mailExec(t, d, `UPDATE sms_templates SET remote_operation='create' WHERE id=$1`, id)
	listed, err := n.ListSMSTemplates(ctx)
	if err != nil || len(listed) != 1 || listed[0].RemoteOperation != "create" {
		t.Fatal("锁定状态未随模板列表返回", err)
	}
	locked := base
	locked.Content = "{{subject}}（锁定后修改）"
	if _, err := n.SaveSMSTemplate(ctx, locked); err == nil {
		t.Fatal("锁定期间仍允许修改模板")
	}
	if err := n.DeleteSMSTemplate(ctx, id); err == nil {
		t.Fatal("锁定期间仍允许删除模板")
	}
	if err := n.RemoteSMSTemplate(ctx, id, "query"); err == nil {
		t.Fatal("锁定期间仍允许重复远程操作")
	}
	if err := n.SaveSMSBinding(ctx, SMSBinding{Code: "ticket_reply", TemplateID: id, Enabled: true}); err == nil {
		t.Fatal("锁定模板仍可被场景绑定")
	}
	// 解锁必须显式确认且不可重复，恢复后不重放任何远程操作。
	if err := n.ResolveSMSTemplateLock(ctx, id+9999); err == nil {
		t.Fatal("为不存在的模板解锁")
	}
	if err := n.ResolveSMSTemplateLock(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := n.ResolveSMSTemplateLock(ctx, id); err == nil {
		t.Fatal("无锁模板被重复解锁")
	}
	unlocked := base
	unlocked.Content = "{{subject}}（解锁后修改）"
	if _, err := n.SaveSMSTemplate(ctx, unlocked); err != nil {
		t.Fatal("解锁后仍无法编辑", err)
	}
}
