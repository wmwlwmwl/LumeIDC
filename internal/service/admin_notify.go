package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

// keyAdminNotifyEmail 管理员告警收件邮箱（settings 表）。
// 支持多个地址，用逗号、分号、空白或换行分隔；留空表示不向管理员发信。
const keyAdminNotifyEmail = "admin_notify_email"

const (
	// MaxAdminAlertRecipients 管理员告警收件人上限，后台保存时按同一个值校验。
	MaxAdminAlertRecipients = 10
	maxAdminAlertSubject    = 200
	maxAdminAlertBody       = 4000
)

// AdminNotifyEmail 返回管理员告警收件邮箱的原始配置值。
func (n *Notifier) AdminNotifyEmail(ctx context.Context) string {
	if n == nil || n.Settings == nil {
		return ""
	}
	v, err := n.Settings.Get(ctx, keyAdminNotifyEmail)
	if err != nil {
		return ""
	}
	return v
}

// SetAdminNotifyEmail 保存管理员告警收件邮箱（不做格式校验，非法地址在发送时被丢弃）。
func (n *Notifier) SetAdminNotifyEmail(ctx context.Context, value string) error {
	if n == nil || n.Settings == nil {
		return errors.New("邮件服务不可用")
	}
	if err := n.Settings.Set(ctx, keyAdminNotifyEmail, strings.TrimSpace(value)); err != nil {
		return errors.New("保存管理员告警邮箱失败")
	}
	return nil
}

// AdminNotifyRecipients 解析管理员告警收件人：支持逗号/分号/空白/换行分隔，
// 逐个用 net/mail 校验、去重、忽略非法地址，并限制总数防止误填整份通讯录。
func (n *Notifier) AdminNotifyRecipients(ctx context.Context) []string {
	return parseAdminNotifyRecipients(n.AdminNotifyEmail(ctx))
}

// parseAdminNotifyRecipients 纯函数形式的收件人解析，便于脱离数据库测试。
func parseAdminNotifyRecipients(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	split := func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, split) {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		parsed, err := mail.ParseAddress(addr)
		if err != nil {
			log.Printf("[admin-alert] 忽略无效的管理员告警邮箱: %q", addr)
			continue
		}
		// 只接受纯地址，拒绝「显示名 <a@b.c>」这类带注入风险的形式。
		if parsed.Address != addr {
			log.Printf("[admin-alert] 忽略格式不合法的管理员告警邮箱: %q", addr)
			continue
		}
		lower := strings.ToLower(addr)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, addr)
		if len(out) >= MaxAdminAlertRecipients {
			break
		}
	}
	return out
}

// adminAlertHTML 把告警正文套进统一邮件外壳。正文按文本转义后放进 pre-wrap 区域，
// 既保留换行缩进，也不会把失败原因里的尖括号当成标签。
func adminAlertHTML(site, body string) string {
	return strings.ReplaceAll(emailHTML(html.EscapeString(body)), "{{site_name}}", html.EscapeString(site))
}

// truncateRunes 按字符截断并追加省略号，避免把超长失败原因写进邮件头/正文。
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// truncateBytes 按字节上限截断（不切断多字节字符）。
// 邮件标题的限制是字节数，中文站点名 + 中文标题很容易在字符数合规时仍超限，
// 超限会让整封告警发送失败——必须按字节截断，而不是让校验把告警拦下来。
func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max - len("…")
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ") + "…"
}

// NotifyAdminOnce 向管理员发送告警邮件，同一 alertKey 全局只发一次。
//
// 去重由 admin_alert_log.alert_key 的唯一索引保证：插入成功才入队邮件，
// 冲突（ON CONFLICT DO NOTHING 无返回行）说明该事件此前已告警过，直接跳过。
// 记账与入队在同一事务内提交，任一步失败都不会留下"已发过"的假象。
//
// 下列两种情况不算"已送达"，因此不消耗去重键——配置补齐后同一事件仍会告警：
//  1. 未配置管理员收件邮箱；
//  2. 业务邮件总开关 notify_email_forward_enabled 关闭。
//
// 尚未配置 SMTP 账号时邮件会留在 mail_outbox 里退避重试，配置生效后自动补发。
func (n *Notifier) NotifyAdminOnce(ctx context.Context, alertKey, category, subject, body string) error {
	if n == nil || n.db == nil {
		return errors.New("通知服务不可用")
	}
	alertKey = strings.TrimSpace(alertKey)
	if alertKey == "" {
		return errors.New("管理员告警缺少去重键")
	}
	subject = truncateRunes(strings.TrimSpace(subject), maxAdminAlertSubject)
	body = truncateRunes(strings.TrimSpace(body), maxAdminAlertBody)
	if subject == "" || body == "" {
		return errors.New("管理员告警标题或正文为空")
	}
	recipients := n.AdminNotifyRecipients(ctx)
	if len(recipients) == 0 {
		return nil
	}
	if !n.EmailForwardEnabled(ctx) {
		log.Printf("[admin-alert] 业务邮件总开关已关闭，跳过告警 %s", alertKey)
		return nil
	}
	site := n.SiteName(ctx)
	if strings.TrimSpace(site) == "" {
		site = DefaultSiteName
	}
	// 站点名前缀也要计入字节上限：validateMailContent 按字节校验标题。
	fullSubject := truncateBytes(site+" "+subject, maxMailSubject)
	fullBody := adminAlertHTML(site, body)
	if err := validateMailContent(fullSubject, fullBody, mailFormatHTML); err != nil {
		return err
	}
	if category == "" {
		category = "system"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("无法开启管理员告警事务")
	}
	defer tx.Rollback()
	var logged int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO admin_alert_log(alert_key,category,subject,body) VALUES($1,$2,$3,$4)
		 ON CONFLICT (alert_key) DO NOTHING RETURNING id`, alertKey, category, subject, body).Scan(&logged)
	if errors.Is(err, sql.ErrNoRows) {
		// 已告警过：同一事件不重复打扰管理员。
		return nil
	}
	if err != nil {
		log.Printf("[admin-alert] 记录告警去重失败，键=%s: %v", alertKey, err)
		return errors.New("记录管理员告警失败")
	}
	for _, to := range recipients {
		if err := validateMailRecipient(to); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mail_outbox(recipient,subject,body,format) VALUES($1,$2,$3,$4)`,
			to, fullSubject, fullBody, mailFormatHTML); err != nil {
			log.Printf("[admin-alert] 告警邮件入队失败，键=%s: %v", alertKey, err)
			return errors.New("管理员告警邮件入队失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("管理员告警事务提交失败")
	}
	n.wakeMail()
	log.Printf("[admin-alert] 告警邮件已入队，收件人 %d 位：%s（%s）", len(recipients), subject, alertKey)
	return nil
}

// adminAlertFields 把若干「标签：值」行拼成邮件正文，空值整行省略。
func adminAlertFields(title string, fields ...[2]string) string {
	var b strings.Builder
	b.WriteString(title)
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			continue
		}
		b.WriteString("\n" + f[0] + "：" + f[1])
	}
	return b.String()
}

// userLabel 生成「#12（a@b.c）」形式的用户标识；查不到邮箱时只留编号。
func userLabel(ctx context.Context, users interface {
	EmailByID(context.Context, int64) (string, error)
}, userID int64) string {
	label := fmt.Sprintf("#%d", userID)
	if users == nil || userID <= 0 {
		return label
	}
	if email, err := users.EmailByID(ctx, userID); err == nil && strings.TrimSpace(email) != "" {
		return label + "（" + strings.TrimSpace(email) + "）"
	}
	return label
}
