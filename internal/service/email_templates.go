package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"html"
	"html/template"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

type EmailTemplateVariable struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Example string `json:"example"`
}

type EmailTemplate struct {
	Code      string                  `json:"code"`
	Name      string                  `json:"name"`
	Category  string                  `json:"category"`
	Required  bool                    `json:"required"`
	Enabled   bool                    `json:"enabled"`
	Subject   string                  `json:"subject"`
	Body      string                  `json:"body"`
	Variables []EmailTemplateVariable `json:"variables"`
	Custom    bool                    `json:"custom"`
}

type EmailTemplateDraft struct {
	Code    string `json:"code"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Enabled bool   `json:"enabled"`
}

type EmailTemplatePreview struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

const (
	mailFormatText  = "text"
	mailFormatHTML  = "html"
	maxMailSubject  = 512
	maxTemplateBody = 256 * 1024
	maxMailBody     = 1024 * 1024
)

// emailHTML 邮件外壳：白卡 + 内联样式，兼容主流邮箱客户端，且不引用任何外部资源（避免被拦截和泄露已读时间）。
// 正文区保留 white-space:pre-wrap，业务文案里的换行与缩进才不会被折叠。
func emailHTML(body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="X-UA-Compatible" content="IE=edge"><meta name="color-scheme" content="light"></head>` +
		// 灰底放在外层单元格上：后台预览会剥掉 body 标签，这样两处观感一致。
		`<body style="margin:0;padding:0;background:#f3f4f6;color:#1f2937;font-family:Arial,'Microsoft YaHei',sans-serif;">` +
		`<table width="100%" border="0" cellpadding="0" cellspacing="0" role="presentation" style="width:100%;border-collapse:collapse;background:#f3f4f6;"><tr><td align="center" style="padding:24px 16px;">` +
		`<table width="100%" border="0" cellpadding="0" cellspacing="0" role="presentation" style="width:100%;max-width:600px;border-collapse:collapse;">` +
		`<tr><td style="text-align:center;background:#ffffff;border-radius:12px;box-shadow:0 4px 12px rgba(0,0,0,0.08);padding:32px;">` +
		`<h1 style="font-size:22px;margin:0 0 24px 0;color:#111827;font-weight:600;">{{site_name}}</h1>` +
		`<div style="font-size:16px;line-height:1.7;color:#374151;white-space:pre-wrap;overflow-wrap:anywhere;">` + body + `</div>` +
		`<hr style="border:0;border-top:1px solid #e5e7eb;margin:24px 0 20px 0;">` +
		`<p style="margin:0;color:#6b7280;font-size:13px;line-height:1.6;">本邮件由 {{site_name}} 系统自动发送，请勿直接回复。<br>如需帮助，请登录网站提交工单联系我们。</p>` +
		`</td></tr></table></td></tr></table></body></html>`
}

// 默认目录只在程序中维护；升级不会覆盖数据库里的自定义。
func defaultEmailTemplates() []EmailTemplate {
	site := EmailTemplateVariable{"site_name", "站点名称", DefaultSiteName}
	message := EmailTemplateVariable{"message", "业务通知原文", "你的业务已处理，请登录账户查看详情。"}
	subject := EmailTemplateVariable{"subject", "工单主题", "服务器连接咨询"}
	amount := EmailTemplateVariable{"amount", "金额（元）", "100.00"}
	invoice := EmailTemplateVariable{"invoice_no", "账单编号", "示例账单20260916001"}
	defs := []struct {
		code, name, category, body string
		vars                       []EmailTemplateVariable
	}{
		// 验证码单独成块：双击只选中验证码本身，不会被相邻文字一起选中。
		{"auth_code", "邮箱验证码", "账户安全",
			"您好，您本次操作的验证码如下：\n" +
				`<div style="background:#eff6ff;border-radius:8px;padding:20px;text-align:center;margin:16px 0;"><span style="font-size:32px;font-weight:bold;color:#2563eb;letter-spacing:6px;">{{code}}</span></div>` + "\n" +
				"验证码有效期：{{minutes}} 分钟，请尽快使用。\n" +
				"请勿向任何人透露验证码。如非本人操作，请直接忽略本邮件。",
			[]EmailTemplateVariable{{"code", "验证码（示例）", "123456"}, {"minutes", "有效分钟数", "5"}}},
		{"ticket_created", "新工单已提交", "工单", "你的工单「{{subject}}」已提交，客服会尽快处理。", []EmailTemplateVariable{subject}},
		{"ticket_reply", "工单收到新回复", "工单", "你的工单「{{subject}}」收到客服新回复，请登录工单中心查看。", []EmailTemplateVariable{subject}},
		{"ticket_assigned", "工单已分配", "工单", "你的工单「{{subject}}」已分配客服处理。", []EmailTemplateVariable{subject}},
		{"ticket_timeout", "工单处理提醒", "工单", "你的工单「{{subject}}」仍在处理中，客服会尽快跟进。", []EmailTemplateVariable{subject}},
		{"payment_success", "支付成功", "财务", "{{message}}", []EmailTemplateVariable{amount, invoice}},
		{"order_submitted", "订单已提交", "财务", "{{message}}", []EmailTemplateVariable{{"order_id", "订单编号", "1001"}, {"product_name", "商品名称", "云服务器"}, amount, invoice}},
		{"recharge_success", "充值成功", "财务", "{{message}}", []EmailTemplateVariable{amount, invoice}},
		{"promotion_ending", "活动即将结束", "营销活动", "{{message}}", []EmailTemplateVariable{{"promotion_name", "活动名称", "限时优惠"}, {"ends_at", "结束时间", "2026-09-17 12:00"}}},
		{"service_expiring", "服务即将到期", "服务", "{{message}}", []EmailTemplateVariable{{"service_id", "服务编号", "1001"}, {"expires_at", "到期时间", "2026-09-19 12:00"}}},
		{"identity_submitted", "实名申请已提交", "实名认证", "{{message}}", nil},
		{"identity_approved", "实名审核已通过", "实名认证", "{{message}}", nil},
		{"identity_rejected", "实名审核未通过", "实名认证", "{{message}}", nil},
		{"cancel_submitted", "停用申请已提交", "停用申请", "{{message}}", nil},
		{"cancel_approved", "停用申请已通过", "停用申请", "{{message}}", nil},
		{"cancel_rejected", "停用申请未通过", "停用申请", "{{message}}", nil},
	}
	out := make([]EmailTemplate, 0, len(defs))
	for _, d := range defs {
		vars := []EmailTemplateVariable{site}
		if d.code != "auth_code" {
			vars = append(vars, message)
		}
		vars = append(vars, d.vars...)
		out = append(out, EmailTemplate{Code: d.code, Name: d.name, Category: d.category, Required: d.code == "auth_code", Enabled: true, Subject: "{{site_name}} " + d.name, Body: emailHTML(d.body), Variables: vars})
	}
	return out
}

func defaultEmailTemplate(code string) (EmailTemplate, error) {
	for _, t := range defaultEmailTemplates() {
		if t.Code == code {
			return t, nil
		}
	}
	return EmailTemplate{}, errors.New("邮件模板不存在")
}

type emailTemplateReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadEmailTemplate(ctx context.Context, db emailTemplateReader, code string) (EmailTemplate, error) {
	t, err := defaultEmailTemplate(code)
	if err != nil {
		return t, err
	}
	var subject, body sql.NullString
	var enabled sql.NullBool
	err = db.QueryRowContext(ctx, `SELECT subject,body,enabled FROM email_templates WHERE code=$1`, code).Scan(&subject, &body, &enabled)
	if err == nil {
		if subject.Valid {
			t.Subject = subject.String
		}
		if body.Valid {
			t.Body = body.String
		}
		if enabled.Valid {
			t.Enabled = enabled.Bool
		}
		t.Custom = subject.Valid || body.Valid || enabled.Valid
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return t, errors.New("读取邮件模板失败")
	}
	// 旧工单配置保留原表及站内信语义；只有从未保存/重置的新邮件模板才回退读取。
	if strings.HasPrefix(code, "ticket_") {
		event := strings.TrimPrefix(code, "ticket_")
		var title, text string
		if err := db.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key=$1),''),coalesce((SELECT value FROM settings WHERE key=$2),'')`, "ticket_notify_"+event+"_title", "ticket_notify_"+event+"_body").Scan(&title, &text); err != nil {
			return t, errors.New("读取旧工单邮件配置失败")
		}
		if strings.TrimSpace(title) != "" && title != t.Name {
			t.Subject = "{{site_name}} " + title
			t.Custom = true
		}
		if strings.TrimSpace(text) != "" {
			// 旧正文只有 subject 会被替换，其余花括号是原样文本，不升级成可执行变量。
			parts := strings.Split(text, "{{subject}}")
			for i := range parts {
				parts[i] = strings.NewReplacer("{", "&#123;", "}", "&#125;").Replace(html.EscapeString(parts[i]))
			}
			legacy := emailHTML(strings.Join(parts, "{{subject}}"))
			if legacy != t.Body {
				t.Body = legacy
				t.Custom = true
			}
		}
	}
	return t, nil
}

func (n *Notifier) ListEmailTemplates(ctx context.Context) ([]EmailTemplate, error) {
	if n == nil || n.db == nil {
		return nil, errors.New("邮件服务不可用")
	}
	out := defaultEmailTemplates()
	for i := range out {
		t, err := loadEmailTemplate(ctx, n.db, out[i].Code)
		if err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

func emailHeaderValid(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func validateMailContent(subject, body, format string) error {
	if !emailHeaderValid(subject) {
		return errors.New("邮件标题不能包含换行或控制字符")
	}
	if strings.TrimSpace(subject) == "" || len(subject) > maxMailSubject {
		return errors.New("邮件标题不能为空且不能超过512字节")
	}
	if !utf8.ValidString(body) || len(body) > maxMailBody {
		return errors.New("邮件正文无效或超过大小限制")
	}
	if format != mailFormatText && format != mailFormatHTML {
		return errors.New("邮件格式无效")
	}
	return nil
}

func validateMailRecipient(to string) error {
	a, err := mail.ParseAddress(to)
	if err != nil || a.Address != to || len(to) > 254 || !emailHeaderValid(to) {
		return errors.New("请输入一个有效的收件邮箱地址")
	}
	return nil
}

// 只接受精确的 {{key}}，先验证再编译，绝不把管理员输入作为模板表达式执行。
func compileEmailText(text string, allowed map[string]bool, htmlBody bool, values map[string]string) (string, error) {
	var out strings.Builder
	for len(text) > 0 {
		i := strings.Index(text, "{{")
		if i < 0 {
			if strings.Contains(text, "}}") {
				return "", errors.New("邮件模板变量格式错误，请使用{{变量名}}")
			}
			out.WriteString(text)
			break
		}
		prefix := text[:i]
		if strings.Contains(prefix, "}}") || (i > 0 && text[i-1] == '{') {
			return "", errors.New("邮件模板变量格式错误")
		}
		out.WriteString(prefix)
		text = text[i+2:]
		j := strings.Index(text, "}}")
		if j < 0 {
			return "", errors.New("邮件模板变量未闭合")
		}
		key := text[:j]
		if !allowed[key] || (len(text) > j+2 && text[j+2] == '}') {
			return "", errors.New("邮件模板包含未知变量或非法表达式")
		}
		if _, ok := values[key]; !ok {
			return "", errors.New("邮件模板缺少业务变量")
		}
		if len(values[key]) > maxMailBody || (!htmlBody && out.Len()+len(values[key]) > maxMailSubject) {
			return "", errors.New("邮件变量渲染后超过大小限制")
		}
		if htmlBody {
			out.WriteString("{{." + key + "}}")
		} else {
			out.WriteString(values[key])
		}
		text = text[j+2:]
	}
	return out.String(), nil
}

type emailBodyBuffer struct{ bytes.Buffer }

func (b *emailBodyBuffer) Write(p []byte) (int, error) {
	if len(p) > maxMailBody-b.Len() {
		return 0, errors.New("邮件正文超过大小限制")
	}
	return b.Buffer.Write(p)
}

func renderEmailTemplate(t EmailTemplate, draft EmailTemplateDraft, values map[string]string) (EmailTemplatePreview, error) {
	var result EmailTemplatePreview
	if t.Required && !draft.Enabled {
		return result, errors.New("验证码邮件模板不可停用")
	}
	if len(draft.Body) > maxTemplateBody || strings.TrimSpace(draft.Body) == "" || !utf8.ValidString(draft.Body) {
		return result, errors.New("邮件模板正文不能为空且不能超过256KB")
	}
	if err := validateMailContent(draft.Subject, draft.Body, mailFormatHTML); err != nil {
		return result, err
	}
	if t.Required && !strings.Contains(draft.Body, "{{code}}") {
		return result, errors.New("验证码邮件正文必须包含{{code}}变量")
	}
	allowed := make(map[string]bool, len(t.Variables))
	for _, v := range t.Variables {
		allowed[v.Key] = true
	}
	subject, err := compileEmailText(draft.Subject, allowed, false, values)
	if err != nil {
		return result, err
	}
	body, err := compileEmailText(draft.Body, allowed, true, values)
	if err != nil {
		return result, err
	}
	tmpl, err := template.New("邮件正文").Option("missingkey=error").Parse(body)
	if err != nil {
		return result, errors.New("邮件正文模板格式错误")
	}
	var buf emailBodyBuffer
	if err := tmpl.Execute(&buf, values); err != nil {
		return result, errors.New("邮件正文渲染失败，请检查HTML结构和变量位置")
	}
	result = EmailTemplatePreview{Subject: subject, Body: buf.String()}
	if err := validateMailContent(result.Subject, result.Body, mailFormatHTML); err != nil {
		return EmailTemplatePreview{}, err
	}
	if t.Required && (values["code"] == "" || !strings.Contains(result.Body, values["code"])) {
		return EmailTemplatePreview{}, errors.New("验证码变量必须出现在邮件正文中")
	}
	return result, nil
}

func (n *Notifier) PreviewEmailTemplate(ctx context.Context, draft EmailTemplateDraft) (EmailTemplatePreview, error) {
	t, err := defaultEmailTemplate(draft.Code)
	if err != nil {
		return EmailTemplatePreview{}, err
	}
	values := make(map[string]string, len(t.Variables))
	for _, v := range t.Variables {
		values[v.Key] = v.Example
	}
	values["site_name"] = n.SiteName(ctx)
	return renderEmailTemplate(t, draft, values)
}

func (n *Notifier) SaveEmailTemplate(ctx context.Context, draft EmailTemplateDraft) error {
	if n == nil || n.db == nil {
		return errors.New("邮件服务不可用")
	}
	if _, err := n.PreviewEmailTemplate(ctx, draft); err != nil {
		return err
	}
	_, err := n.db.ExecContext(ctx, `INSERT INTO email_templates(code,subject,body,enabled) VALUES($1,$2,$3,$4) ON CONFLICT(code) DO UPDATE SET subject=excluded.subject,body=excluded.body,enabled=excluded.enabled,updated_at=now()`, draft.Code, draft.Subject, draft.Body, draft.Enabled)
	if err != nil {
		return errors.New("保存邮件模板失败，原模板未更改")
	}
	return nil
}

func (n *Notifier) ResetEmailTemplate(ctx context.Context, code string) error {
	if _, err := defaultEmailTemplate(code); err != nil {
		return err
	}
	if n == nil || n.db == nil {
		return errors.New("邮件服务不可用")
	}
	// 空覆盖标记保证显式恢复默认后不再回退到旧工单自定义；不删除旧设置。
	_, err := n.db.ExecContext(ctx, `INSERT INTO email_templates(code) VALUES($1) ON CONFLICT(code) DO UPDATE SET subject=NULL,body=NULL,enabled=NULL,updated_at=now()`, code)
	if err != nil {
		return errors.New("恢复邮件默认模板失败，原模板未更改")
	}
	return nil
}

func (n *Notifier) TestEmailTemplate(ctx context.Context, to string, draft EmailTemplateDraft) error {
	if err := validateMailRecipient(to); err != nil {
		return err
	}
	ctx, done, err := n.beginMail(ctx, 15*time.Second)
	if err != nil {
		return err
	}
	defer done()
	preview, err := n.PreviewEmailTemplate(ctx, draft)
	if err != nil {
		return err
	}
	// 草稿停用仅影响正式业务投递，测试必须真实发送并报告结果。
	if err := n.sendWithChannels(ctx, to, preview.Subject, preview.Body, mailFormatHTML); err != nil {
		return errors.New("测试邮件发送失败，请检查邮件服务配置或稍后重试")
	}
	return nil
}

// SendEmailCode 同步投递，验证码只存在于内存，不进入持久队列或日志。
func (n *Notifier) SendEmailCode(ctx context.Context, to, code string) error {
	ctx, done, err := n.beginMail(ctx, mailSendTimeout)
	if err != nil {
		return err
	}
	defer done()
	if n.db == nil {
		return errors.New("邮件服务不可用")
	}
	t, err := loadEmailTemplate(ctx, n.db, "auth_code")
	if err != nil {
		return err
	}
	preview, err := renderEmailTemplate(t, EmailTemplateDraft{Code: t.Code, Subject: t.Subject, Body: t.Body, Enabled: t.Enabled}, map[string]string{"site_name": n.SiteName(ctx), "code": code, "minutes": "5"})
	if err != nil {
		return err
	}
	if err := n.sendWithChannels(ctx, to, preview.Subject, preview.Body, mailFormatHTML); err != nil {
		return errors.New("验证码邮件发送失败，请稍后重试")
	}
	return nil
}

// NotifyTemplate 保留站内信原文/原分类，仅邮件使用独立模板及变量。
func (n *Notifier) NotifyTemplate(ctx context.Context, userID int64, code, title, body string, variables ...map[string]string) error {
	if _, err := defaultEmailTemplate(code); err != nil {
		return err
	}
	values := map[string]string{"message": body}
	for _, vars := range variables {
		for k, v := range vars {
			values[k] = v
		}
	}
	return n.notify(ctx, userID, title, body, code, values)
}
