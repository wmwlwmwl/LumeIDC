package service

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/repo"
)

// Notifier 站内信 + 邮件通知（多 SMTP 账号：轮流负载 + 失败冷却切换）。
type Notifier struct {
	db       *sql.DB
	Settings *repo.Settings

	mu               sync.Mutex
	roundRobinCursor int               // 上次成功账号的下一个，用于轮流负载
	cooldownUntil    map[int]time.Time // 账号索引 -> 冷却截止时间（进程内存，重启归零）
}

const (
	keySMTPAccounts = "smtp_accounts"
	keySMTPCooldown = "smtp_cooldown_seconds"
)

// MailAccount 单个 SMTP 账号（settings 表 smtp_accounts JSON 存储）。
type MailAccount struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Pass    string `json:"pass"`
	From    string `json:"from"`
	Enabled bool   `json:"enabled"`
}

// accountName 便于错误提示的可读名称。
func (a MailAccount) accountName() string {
	if s := strings.TrimSpace(a.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.User); s != "" {
		return s
	}
	return a.Host
}

func (a *MailAccount) fromAddress() string {
	if s := strings.TrimSpace(a.From); s != "" {
		return s
	}
	return strings.TrimSpace(a.User)
}

// SMTPConfig 从 settings 表读取的邮件配置（兼容旧单组字段的迁移来源）。
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func (n *Notifier) loadSMTP(ctx context.Context) SMTPConfig {
	get := func(k string) string {
		v, _ := n.Settings.Get(ctx, k)
		return v
	}
	port, _ := strconv.Atoi(get("smtp_port"))
	if port == 0 {
		port = 587
	}
	return SMTPConfig{
		Host: get("smtp_host"),
		Port: port,
		User: get("smtp_user"),
		Pass: get("smtp_pass"),
		From: get("smtp_from"),
	}
}

// loadAccounts 读取多账号列表（保持存储顺序）；为空时回退单组 smtp_* 作为账号0（旧配置迁移）。
func (n *Notifier) loadAccounts(ctx context.Context) []MailAccount {
	if n.Settings != nil {
		if raw, err := n.Settings.Get(ctx, keySMTPAccounts); err == nil && strings.TrimSpace(raw) != "" {
			var list []MailAccount
			if json.Unmarshal([]byte(raw), &list) == nil {
				for i := range list {
					if list[i].Port == 0 {
						list[i].Port = 587
					}
				}
				return list
			}
		}
	}
	legacy := n.loadSMTP(ctx)
	if strings.TrimSpace(legacy.Host) == "" {
		return nil
	}
	acct := MailAccount{
		Name:    "默认账号",
		Host:    legacy.Host,
		Port:    legacy.Port,
		User:    legacy.User,
		Pass:    legacy.Pass,
		From:    legacy.From,
		Enabled: true,
	}
	if acct.Port == 0 {
		acct.Port = 587
	}
	return []MailAccount{acct}
}

// hasEnabledAccount 是否存在启用的可发信账号。
func (n *Notifier) hasEnabledAccount(ctx context.Context) bool {
	for _, a := range n.loadAccounts(ctx) {
		if a.Enabled && strings.TrimSpace(a.Host) != "" {
			return true
		}
	}
	return false
}

// cooldownSeconds 读取失败冷却秒数（默认 60）。
func (n *Notifier) cooldownSeconds(ctx context.Context) int {
	sec := 60
	if n.Settings != nil {
		if v, err := n.Settings.Get(ctx, keySMTPCooldown); err == nil {
			if p, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && p >= 1 {
				sec = p
			}
		}
	}
	if sec > 86400 {
		sec = 86400
	}
	return sec
}

// notifyEmailEnabled 站内信同步到邮箱的独立开关；兼容旧版 notify_email_enabled=1 的存量配置。
func (n *Notifier) notifyEmailEnabled(ctx context.Context) bool {
	if n == nil || n.Settings == nil {
		return false
	}
	v, _ := n.Settings.Get(ctx, "notify_email_forward_enabled")
	if v == "1" {
		return true
	}
	if v == "" {
		old, _ := n.Settings.Get(ctx, "notify_email_enabled")
		return old == "1"
	}
	return false
}

// Notify 写站内信；若开启“站内信同步到邮箱”则尝试向用户邮箱发同内容邮件。任何失败仅记日志，不阻断主流程。
func (n *Notifier) Notify(ctx context.Context, userID int64, title, body string) {
	if n == nil || n.db == nil {
		return
	}
	if _, err := n.db.ExecContext(ctx,
		`INSERT INTO notifications(user_id,title,body) VALUES($1,$2,$3)`, userID, title, body); err != nil {
		log.Printf("[notify] 站内信写入失败 user=%d: %v", userID, err)
	}
	if !n.notifyEmailEnabled(ctx) || !n.hasEnabledAccount(ctx) {
		return
	}
	var email sql.NullString
	if err := n.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err != nil || !email.Valid || email.String == "" {
		return
	}
	if err := n.sendWithChannels(ctx, email.String, title, body); err != nil {
		log.Printf("[notify] 邮件发送失败 user=%d: %v", userID, err)
	}
}

// EmailEnabled 返回是否已配置启用的 SMTP 账号（注册邮箱验证码等强校验用；与“站内信同步”开关无关）。
func (n *Notifier) EmailEnabled(ctx context.Context) bool {
	return n != nil && n.hasEnabledAccount(ctx)
}

// buildMailMessage 拼装带 MIME 头的中文邮件内容。
func buildMailMessage(from, to, subject, body string) []byte {
	return []byte("To: " + to + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
}

// loginAuth 实现 SMTP AUTH LOGIN（部分服务器不提供 PLAIN 时使用）。
type loginAuth struct {
	user, pass string
}

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if len(fromServer) == 0 {
		return []byte(a.user), nil
	}
	prompt := string(fromServer)
	if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(prompt)); err == nil {
		prompt = string(dec)
	}
	switch strings.ToLower(strings.TrimSpace(prompt)) {
	case "username", "username:":
		return []byte(a.user), nil
	case "password", "password:":
		return []byte(a.pass), nil
	}
	return nil, nil
}

// smtpAuth 依次尝试服务器支持的 AUTH 机制（PLAIN / LOGIN）。
func smtpAuth(c *smtp.Client, host, user, pass string) error {
	methods := ""
	if ok, ex := c.Extension("AUTH"); ok {
		methods = strings.ToUpper(ex)
	}
	has := func(name string) bool { return strings.Contains(methods, name) }
	if !has("LOGIN") {
		err := c.Auth(smtp.PlainAuth("", user, pass, host))
		if err == nil {
			return nil
		}
		if has("PLAIN") || methods == "" {
			return fmt.Errorf("用户名/密码可能不正确，或被服务器拒绝: %v", err)
		}
		return err
	}
	// 优先 LOGIN：兼容只放行 LOGIN 的服务器
	if err := c.Auth(&loginAuth{user: user, pass: pass}); err == nil {
		return nil
	}
	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err == nil {
		return nil
	}
	return fmt.Errorf("用户名/密码可能不正确（PLAIN/LOGIN 均被拒绝），请核对后重试")
}

// smtpDeliver 发送一封邮件：465 端口走隐式 TLS（SMTPS），其余端口按需 STARTTLS。
// 带 10s 连接与 25s 会话超时，避免主机不可达/无响应时无限阻塞。
func smtpDeliver(host string, port int, user, pass, from, to string, msg []byte) error {
	if host == "" {
		return fmt.Errorf("SMTP 主机为空")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("无法连接 %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(25 * time.Second))
	defer conn.SetDeadline(time.Time{})

	var c *smtp.Client
	if port == 465 {
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tc.Handshake(); err != nil {
			return fmt.Errorf("TLS 握手失败（端口 %d）: %w", port, err)
		}
		c, err = smtp.NewClient(tc, host)
	} else {
		c, err = smtp.NewClient(conn, host)
	}
	if err != nil {
		return fmt.Errorf("建立 SMTP 会话失败: %w", err)
	}
	defer func() { _ = c.Quit() }()

	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("STARTTLS 升级失败: %w", err)
			}
		}
	}
	if user != "" || pass != "" {
		if err := smtpAuth(c, host, user, pass); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 被拒绝: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO 被拒绝（收件地址或发送权限问题）: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("进入 DATA 阶段失败: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("邮件投递被服务器拒绝: %w", err)
	}
	return nil
}

// SendMail 发送纯文本邮件（自动走多账号轮流负载/失败切换）。
func (n *Notifier) SendMail(ctx context.Context, to, subject, body string) error {
	return n.sendWithChannels(ctx, to, subject, body)
}

// sendWithChannels 多账号轮流负载发送：
//   - 每封从“上次成功账号的下一个”开始（round robin）；
//   - 发送失败的账号进入冷却，本次自动尝试下一个可用账号；
//   - 冷却中的账号跳过；全部失败返回汇总错误。
func (n *Notifier) sendWithChannels(ctx context.Context, to, subject, body string) error {
	if n == nil || n.db == nil || n.Settings == nil {
		return fmt.Errorf("邮件服务不可用")
	}
	accounts := n.loadAccounts(ctx)
	if len(accounts) == 0 {
		return fmt.Errorf("尚未配置 SMTP 账号，请先在「系统设置-邮件服务」填写并保存")
	}
	if len(accounts) == 1 {
		return n.sendAccount(ctx, 0, to, subject, body, false)
	}

	cooldown := n.cooldownSeconds(ctx)
	total := len(accounts)

	// 短锁取候选顺序：从 cursor 开始、跳过未启用/冷却中的账号
	n.mu.Lock()
	if n.cooldownUntil == nil {
		n.cooldownUntil = map[int]time.Time{}
	}
	now := time.Now()
	candidates := make([]int, 0, total)
	for i := 0; i < total; i++ {
		idx := (n.roundRobinCursor + i) % total
		if !accounts[idx].Enabled {
			continue
		}
		if until, ok := n.cooldownUntil[idx]; ok && until.After(now) {
			continue
		}
		candidates = append(candidates, idx)
	}
	n.mu.Unlock()

	if len(candidates) == 0 {
		return fmt.Errorf("暂无可用 SMTP 账号（可能均在失败冷却中），请稍后重试")
	}

	var lastErr error
	for _, idx := range candidates {
		if err := n.sendAccount(ctx, idx, to, subject, body, false); err != nil {
			lastErr = err
			n.mu.Lock()
			n.cooldownUntil[idx] = time.Now().Add(time.Duration(cooldown) * time.Second)
			n.mu.Unlock()
			continue
		}
		n.mu.Lock()
		n.roundRobinCursor = (idx + 1) % total
		delete(n.cooldownUntil, idx)
		n.mu.Unlock()
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用 SMTP 账号")
	}
	return fmt.Errorf("已尝试 %d 个账号均失败，最后失败：%v", len(candidates), lastErr)
}

// sendAccount 用指定索引的账号直接发送。ignoreEnabled=true 时忽略启用开关（用于定向测试）。
func (n *Notifier) sendAccount(ctx context.Context, idx int, to, subject, body string, ignoreEnabled bool) error {
	accounts := n.loadAccounts(ctx)
	if idx < 0 || idx >= len(accounts) {
		return fmt.Errorf("SMTP 账号 #%d 不存在", idx+1)
	}
	acct := accounts[idx]
	if !ignoreEnabled && !acct.Enabled {
		return fmt.Errorf("账号 #%d「%s」未启用", idx+1, acct.accountName())
	}
	host := strings.TrimSpace(acct.Host)
	if host == "" {
		return fmt.Errorf("账号 #%d「%s」未填写 SMTP 主机", idx+1, acct.accountName())
	}
	from := acct.fromAddress()
	if from == "" {
		return fmt.Errorf("账号 #%d「%s」未填写用户名/发件人", idx+1, acct.accountName())
	}
	msg := buildMailMessage(from, to, subject, body)
	if err := smtpDeliver(host, acct.Port, acct.User, acct.Pass, from, to, msg); err != nil {
		return fmt.Errorf("账号 #%d「%s」发送失败: %w", idx+1, acct.accountName(), err)
	}
	return nil
}

// runSendTest 统一带 15s 超时执行测试发送：idx<0 走整链路轮流负载，否则定向测试该账号（无视冷却与启用开关）。
func (n *Notifier) runSendTest(ctx context.Context, idx int, to, subject, body string) error {
	if n == nil || n.db == nil || n.Settings == nil {
		return fmt.Errorf("邮件服务不可用")
	}
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		if idx >= 0 {
			done <- result{n.sendAccount(ctx, idx, to, subject, body, true)}
			return
		}
		done <- result{n.sendWithChannels(ctx, to, subject, body)}
	}()
	select {
	case r := <-done:
		return r.err
	case <-time.After(15 * time.Second):
		return fmt.Errorf("发送超时（15 秒未响应）：请检查 SMTP 主机、端口是否可达，账号密码是否允许外部应用发信")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SendTestMail 测试整条发送链路（轮流负载 + 失败切换）。
func (n *Notifier) SendTestMail(ctx context.Context, to, subject, body string) error {
	return n.runSendTest(ctx, -1, to, subject, body)
}

// SendTestMailAccount 定向测试第 idx 个 SMTP 账号。
func (n *Notifier) SendTestMailAccount(ctx context.Context, idx int, to, subject, body string) error {
	return n.runSendTest(ctx, idx, to, subject, body)
}

// MarkNotificationsRead 标记用户全部站内信为已读。
func (n *Notifier) MarkRead(ctx context.Context, userID int64) error {
	_, err := n.db.ExecContext(ctx, `UPDATE notifications SET read=true WHERE user_id=$1`, userID)
	return err
}

// UserNotifications 取用户站内信（最新在前）。
func (n *Notifier) List(ctx context.Context, userID int64) ([]map[string]any, error) {
	rows, err := n.db.QueryContext(ctx,
		`SELECT id,title,body,read,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM notifications
		 WHERE user_id=$1 ORDER BY id DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var title, body, t string
		var read bool
		if err := rows.Scan(&id, &title, &body, &read, &t); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "title": title, "body": body, "read": read, "time": t})
	}
	return out, rows.Err()
}
