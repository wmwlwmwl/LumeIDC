package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
)

func templateDraft(t EmailTemplate) EmailTemplateDraft {
	return EmailTemplateDraft{Code: t.Code, Subject: t.Subject, Body: t.Body, Enabled: t.Enabled}
}

func TestEmailTemplateCatalogAndValidation(t *testing.T) {
	n := &Notifier{}
	seen := map[string]bool{}
	for _, item := range defaultEmailTemplates() {
		if seen[item.Code] || len(item.Variables) == 0 {
			t.Fatal("模板目录重复或缺少变量")
		}
		seen[item.Code] = true
		if _, err := n.PreviewEmailTemplate(context.Background(), templateDraft(item)); err != nil {
			t.Fatalf("默认模板%s无效：%v", item.Code, err)
		}
		for _, v := range item.Variables {
			if v.Label == "" || v.Example == "" {
				t.Fatal("变量缺少中文标签或例值")
			}
		}
	}
	if len(seen) != 16 {
		t.Fatal("业务模板目录不完整")
	}
	base, _ := defaultEmailTemplate("payment_success")
	for _, body := range []string{"{{unknown}}", "{{ message }}", "{{.message}}", "{{message | html}}", "{{if message}}", "{{message}", "{{{message}}}", "{{message}}}", "孤立}}"} {
		draft := templateDraft(base)
		draft.Body = body
		if _, err := n.PreviewEmailTemplate(context.Background(), draft); err == nil {
			t.Fatalf("未拒绝错误表达式：%s", body)
		}
	}
	for _, subject := range []string{"标题\r\n密送: x", "标题\x00", strings.Repeat("a", maxMailSubject+1)} {
		draft := templateDraft(base)
		draft.Subject = subject
		if _, err := n.PreviewEmailTemplate(context.Background(), draft); err == nil {
			t.Fatal("未拒绝无效标题")
		}
	}
	if _, err := n.PreviewEmailTemplate(context.Background(), EmailTemplateDraft{Code: "不存在"}); err == nil {
		t.Fatal("未拒绝未知模板")
	}
	auth, _ := defaultEmailTemplate("auth_code")
	for _, draft := range []EmailTemplateDraft{
		{Code: auth.Code, Subject: auth.Subject, Body: auth.Body, Enabled: false},
		{Code: auth.Code, Subject: auth.Subject, Body: "没有验证码", Enabled: true},
		{Code: auth.Code, Subject: auth.Subject, Body: "<!-- {{code}} -->", Enabled: true},
	} {
		if _, err := n.PreviewEmailTemplate(context.Background(), draft); err == nil {
			t.Fatal("验证码必需约束未生效")
		}
	}
}

func TestEmailTemplateSizeAndRecipient(t *testing.T) {
	for _, to := range []string{"a@x.test,b@x.test", "收件人 <a@x.test>", "a@x.test\r\nBcc: b@x.test", "无效邮箱"} {
		if validateMailRecipient(to) == nil {
			t.Fatal("未拒绝无效或多个收件地址")
		}
	}
	item, _ := defaultEmailTemplate("payment_success")
	draft := templateDraft(item)
	draft.Body = "{{message}}{{message}}"
	if _, err := renderEmailTemplate(item, draft, map[string]string{"site_name": "测试", "message": strings.Repeat("a", maxMailBody)}); err == nil {
		t.Fatal("未限制渲染展开大小")
	}
	draft.Body = strings.Repeat("a", maxTemplateBody+1)
	if _, err := (&Notifier{}).PreviewEmailTemplate(context.Background(), draft); err == nil {
		t.Fatal("未限制模板大小")
	}
}

func TestEmailTemplateEscapesVariables(t *testing.T) {
	item, _ := defaultEmailTemplate("ticket_reply")
	draft := templateDraft(item)
	draft.Body = `<p title="{{subject}}">{{message}}</p><a href="{{subject}}">查看</a>`
	values := map[string]string{"site_name": "测试站点", "subject": `javascript:alert("注入")`, "message": `<img src=x onerror=alert(1)> & {{site_name}}`}
	got, err := renderEmailTemplate(item, draft, values)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Body, "<img") || !strings.Contains(got.Body, "&lt;img") || !strings.Contains(got.Body, "#ZgotmplZ") || !strings.Contains(got.Body, "{{site_name}}") {
		t.Fatal("变量未安全转义或被二次执行")
	}
	values["site_name"] = "站点\r\nBcc: attacker@example.test"
	if _, err := renderEmailTemplate(item, draft, values); err == nil {
		t.Fatal("渲染后的标题换行未被拒绝")
	}
}

func TestEmailMIMEHTMLAndLegacyText(t *testing.T) {
	body := "<p>中文 &amp; 内容</p>"
	for _, format := range []string{mailFormatText, mailFormatHTML} {
		var data []byte
		if format == mailFormatText {
			data = buildMailMessage("from@x.test", "to@x.test", "中文标题", body)
		} else {
			data = buildMailMessage("from@x.test", "to@x.test", "中文标题", body, format)
		}
		msg, err := mail.ReadMessage(strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		reader := msg.Body
		if format == mailFormatHTML {
			if !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/html;") {
				t.Fatal("HTML MIME类型错误")
			}
			if msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
				t.Fatal("HTML未使用安全传输编码")
			}
			reader = quotedprintable.NewReader(reader)
		} else if !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/plain;") {
			t.Fatal("旧调用不再是纯文本")
		}
		got, err := io.ReadAll(reader)
		if err != nil || string(got) != body {
			t.Fatal("MIME正文不一致")
		}
	}
}

func TestEmailHTMLSMTPDelivery(t *testing.T) {
	srv := startFakeSMTP(t, "", false)
	host, portText, _ := net.SplitHostPort(srv.addr())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	n := &Notifier{}
	if err := n.sendWithAccounts(context.Background(), []MailAccount{{Enabled: true, Host: host, Port: port, From: "from@x.test"}}, "to@x.test", "HTML邮件测试", "<p>中文正文</p>", mailFormatHTML); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	message := srv.gotMessage
	srv.mu.Unlock()
	msg, err := mail.ReadMessage(strings.NewReader(message))
	if err != nil || !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/html;") {
		t.Fatal("SMTP实际投递丢失HTML格式")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || strings.TrimSpace(string(body)) != "<p>中文正文</p>" {
		t.Fatal("SMTP实际投递正文错误")
	}
}

func TestEmailTemplateDisabledSnapshotAndReset(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	ctx := context.Background()
	item, _ := defaultEmailTemplate("payment_success")
	draft := templateDraft(item)
	draft.Enabled = false
	draft.Body = "<h2>管理员自定义{{message}}</h2>"
	if err := n.SaveEmailTemplate(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := n.NotifyTemplate(ctx, uid, item.Code, "支付成功", "原始通知"); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "notifications") != 1 || mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("模板停用影响站内信或仍产生邮件")
	}
	draft.Enabled = true
	if err := n.SaveEmailTemplate(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := n.NotifyTemplate(ctx, uid, item.Code, "支付成功", "原始通知"); err != nil {
		t.Fatal(err)
	}
	var body, category string
	if err := d.QueryRow(`SELECT body,category FROM notifications ORDER BY id DESC LIMIT 1`).Scan(&body, &category); err != nil || body != "原始通知" || category != "payment" {
		t.Fatal("管理员HTML污染站内信或改变分类")
	}
	if err := n.ResetEmailTemplate(ctx, item.Code); err != nil {
		t.Fatal(err)
	}
	job, err := n.claimMail(ctx)
	if err != nil || job.format != mailFormatHTML || !strings.Contains(job.body, "管理员自定义原始通知") {
		t.Fatal("重置改变已入队快照或格式丢失")
	}
	if err := n.finishMail(ctx, job, true); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(ctx, uid, "旧通知", "<b>旧纯文本</b>"); err != nil {
		t.Fatal(err)
	}
	job, err = n.claimMail(ctx)
	if err != nil || job.format != mailFormatText {
		t.Fatal("旧调用未保留纯文本")
	}
	if err := n.Settings.Set(ctx, "ticket_notify_reply_body", "旧自定义：{{subject}} <b>纯文本</b>"); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadEmailTemplate(ctx, d, "ticket_reply")
	if err != nil || !legacy.Custom || !strings.Contains(legacy.Body, "&lt;b&gt;") {
		t.Fatal("旧工单邮件语义未保留")
	}
	if err := n.ResetEmailTemplate(ctx, "ticket_reply"); err != nil {
		t.Fatal(err)
	}
	reset, err := loadEmailTemplate(ctx, d, "ticket_reply")
	if err != nil || reset.Custom || strings.Contains(reset.Body, "旧自定义") {
		t.Fatal("明确恢复默认仍读到旧覆盖")
	}
	old, err := n.Settings.Get(ctx, "ticket_notify_reply_body")
	if err != nil || !strings.Contains(old, "旧自定义") {
		t.Fatal("重置破坏旧站内信设置")
	}
}

func TestEmailTemplateDraftActuallySendsWhenDisabled(t *testing.T) {
	d := mailTestDB(t)
	n, _ := mailTestNotifier(t, d)
	ctx := context.Background()
	srv := startFakeSMTP(t, "", false)
	host, portText, _ := net.SplitHostPort(srv.addr())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	accounts, _ := json.Marshal([]MailAccount{{Enabled: true, Host: host, Port: port, From: "from@x.test"}})
	if err := n.Settings.Set(ctx, keySMTPAccounts, string(accounts)); err != nil {
		t.Fatal(err)
	}
	item, _ := defaultEmailTemplate("payment_success")
	draft := templateDraft(item)
	draft.Enabled = false
	if err := n.TestEmailTemplate(ctx, "to@x.test,other@x.test", draft); err == nil {
		t.Fatal("多收件人未拒绝")
	}
	if err := n.TestEmailTemplate(ctx, "to@x.test", draft); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	got := srv.gotTo
	srv.mu.Unlock()
	if got != "to@x.test" {
		t.Fatal("停用草稿测试被静默跳过")
	}
	if mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("测试邮件不应入队")
	}
}
