package service

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
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
	initOnce         sync.Once
	startOnce        sync.Once
	stopOnce         sync.Once
	mailCtx          context.Context
	mailCancel       context.CancelFunc
	wake             chan struct{}
	stopped          bool
	workers          sync.WaitGroup
	done             chan struct{}
	smsStartOnce     sync.Once
	smsStopOnce      sync.Once
	smsMu            sync.Mutex
	smsCancel        context.CancelFunc
	smsDone          chan struct{}
	smsStopped       bool
}

const (
	keySMTPAccounts = "smtp_accounts"
	keySMTPCooldown = "smtp_cooldown_seconds"
	// keyEmailForwardEnabled 业务邮件总开关，后台「邮件模板」页维护；验证码与短信不受其影响。
	keyEmailForwardEnabled = "notify_email_forward_enabled"
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
		if n.Settings == nil {
			return ""
		}
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
	raw := ""
	if n.Settings != nil {
		raw, _ = n.Settings.Get(ctx, keySMTPAccounts)
	}
	return n.parseAccounts(raw, ctx)
}

// parseAccounts 解析 smtp_accounts JSON；为空/解析失败时回退单组 smtp_*（旧配置迁移）。
func (n *Notifier) parseAccounts(raw string, ctx context.Context) []MailAccount {
	if strings.TrimSpace(raw) != "" {
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

// Notify 在短事务中一起保存站内信和邮件意图；返回成功才代表可靠入队。
// 邮件快照不关联站内信外键、不保存 SMTP 凭据；账号在实际发送时读取。
func (n *Notifier) Notify(ctx context.Context, userID int64, title, body string) error {
	return n.notify(ctx, userID, title, body, "", nil)
}

func (n *Notifier) notify(ctx context.Context, userID int64, title, body, code string, values map[string]string) (err error) {
	if n == nil || n.db == nil {
		return fmt.Errorf("通知服务不可用")
	}
	defer func() {
		if err != nil {
			log.Printf("通知保存失败，用户编号=%d，请重试: %v", userID, err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("无法开启通知事务")
	}
	defer tx.Rollback()
	var notificationID int64
	if err = tx.QueryRowContext(ctx,
		`INSERT INTO notifications(user_id,title,body,category) VALUES($1,$2,$3,$4) RETURNING id`, userID, title, body, notificationCategory(title)).Scan(&notificationID); err != nil {
		return fmt.Errorf("站内通知保存失败")
	}
	var enabled, site, email string
	if err = tx.QueryRowContext(ctx, `SELECT
		coalesce((SELECT value FROM settings WHERE key=$2),''),
		coalesce((SELECT value FROM settings WHERE key='site_name'),''),
		coalesce((SELECT email FROM users WHERE id=$1),'')`, userID, keyEmailForwardEnabled).Scan(&enabled, &site, &email); err != nil {
		return fmt.Errorf("读取通知设置或收件地址失败")
	}
	if enabled == "1" && strings.TrimSpace(email) != "" {
		site = strings.TrimSpace(site)
		if site == "" {
			site = DefaultSiteName
		}
		subject, text := title, body
		if !strings.Contains(title, site) {
			subject = site + " " + title
		}
		if !strings.Contains(body, site) {
			text += "\r\n\r\n—— " + site
		}
		format, send := mailFormatText, true
		if code != "" {
			t, loadErr := loadEmailTemplate(ctx, tx, code)
			if loadErr != nil {
				return loadErr
			}
			send = t.Enabled
			if send {
				values["site_name"] = site
				preview, renderErr := renderEmailTemplate(t, EmailTemplateDraft{Code: t.Code, Subject: t.Subject, Body: t.Body, Enabled: t.Enabled}, values)
				if renderErr != nil {
					return renderErr
				}
				subject, text, format = preview.Subject, preview.Body, mailFormatHTML
			}
		}
		if send {
			if err := validateMailRecipient(email); err != nil {
				return err
			}
			if err := validateMailContent(subject, text, format); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO mail_outbox(recipient,subject,body,format) VALUES($1,$2,$3,$4)`, email, subject, text, format); err != nil {
				return fmt.Errorf("待发邮件保存失败")
			}
		}
	}
	if code != "" && code != "auth_code" {
		if err = n.enqueueSMS(ctx, tx, notificationID, userID, code, values); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("通知事务提交失败")
	}
	n.wakeMail()
	return nil
}

// TicketNotify renders the configurable ticket message template before delivery.
func (n *Notifier) TicketNotify(ctx context.Context, userID int64, event, subject string) {
	if n == nil || n.Settings == nil {
		return
	}
	title, _ := n.Settings.Get(ctx, "ticket_notify_"+event+"_title")
	body, _ := n.Settings.Get(ctx, "ticket_notify_"+event+"_body")
	if strings.TrimSpace(title) == "" {
		title = "工单通知"
	}
	if strings.TrimSpace(body) == "" {
		body = "你的工单「{{subject}}」有新的处理动态。"
	}
	body = strings.ReplaceAll(body, "{{subject}}", subject)
	n.NotifyTemplate(ctx, userID, "ticket_"+event, title, body, map[string]string{"subject": subject})
}

func notificationCategory(title string) string {
	switch {
	case strings.Contains(title, "支付"), strings.Contains(title, "充值"), strings.Contains(title, "退款"):
		return "payment"
	case strings.Contains(title, "服务"), strings.Contains(title, "开通"), strings.Contains(title, "续费"), strings.Contains(title, "升降级"):
		return "service"
	case strings.Contains(title, "实名"):
		return "identity"
	case strings.Contains(title, "订单"), strings.Contains(title, "下单"):
		return "order"
	default:
		return "system"
	}
}

// EmailEnabled 返回是否已配置启用的 SMTP 账号（注册邮箱验证码等强校验用；与“站内信同步”开关无关）。
func (n *Notifier) EmailEnabled(ctx context.Context) bool {
	return n != nil && n.hasEnabledAccount(ctx)
}

// EmailForwardEnabled 业务邮件总开关：关闭后所有业务通知邮件不再入队，站内信照常保存。
// 验证码走同步发送、短信走独立模板，均不受该开关影响。
func (n *Notifier) EmailForwardEnabled(ctx context.Context) bool {
	if n == nil || n.Settings == nil {
		return false
	}
	v, err := n.Settings.Get(ctx, keyEmailForwardEnabled)
	return err == nil && v == "1"
}

// SetEmailForwardEnabled 保存业务邮件总开关。
func (n *Notifier) SetEmailForwardEnabled(ctx context.Context, enabled bool) error {
	if n == nil || n.Settings == nil {
		return errors.New("邮件服务不可用")
	}
	v := "0"
	if enabled {
		v = "1"
	}
	if err := n.Settings.Set(ctx, keyEmailForwardEnabled, v); err != nil {
		return errors.New("保存邮件通知总开关失败")
	}
	return nil
}

// mailHeaderFrom 拼 From 头的发件人：显示名取站点名（形如「无名云 <admin@x.com>」）。
// 只写地址时邮箱客户端会拿邮箱前缀当发件人显示（如「admin」），收件人看不出是谁发的。
// 中文显示名的 RFC 2047 编码与特殊字符加引号交给 net/mail；信封发件人仍是纯地址。
func mailHeaderFrom(site, from string) string {
	if site = strings.TrimSpace(site); site == "" {
		return from
	}
	return (&mail.Address{Name: site, Address: from}).String()
}

func mailFormat(formats []string) string {
	if len(formats) == 0 {
		return mailFormatText
	}
	return formats[0]
}

// buildMailMessage 未传格式的旧调用保持纯文本，HTML 显式使用安全传输编码。
func buildMailMessage(from, to, subject, body string, formats ...string) []byte {
	contentType := "text/plain"
	encoding := ""
	if mailFormat(formats) == mailFormatHTML {
		contentType = "text/html"
		var encoded strings.Builder
		writer := quotedprintable.NewWriter(&encoded)
		_, _ = writer.Write([]byte(body))
		_ = writer.Close()
		body = encoded.String()
		encoding = "Content-Transfer-Encoding: quoted-printable\r\n"
	}
	return []byte("To: " + to + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" + encoding +
		"Content-Type: " + contentType + "; charset=UTF-8\r\n\r\n" + body)
}

// loginAuth 实现 SMTP AUTH LOGIN（部分服务器不提供 PLAIN 时使用）。
type loginAuth struct {
	user, pass string
}

func (a *loginAuth) Start(info *smtp.ServerInfo) (string, []byte, error) {
	if !info.TLS && info.Name != "localhost" && !net.ParseIP(info.Name).IsLoopback() {
		return "", nil, fmt.Errorf("禁止通过未加密连接发送邮箱凭据")
	}
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
func smtpDeliver(ctx context.Context, host string, port int, user, pass, from, to string, msg []byte) (err error) {
	defer func() {
		if err == nil {
			return // DATA 已确认接收，不因随后取消而误报失败。
		}
		if ctx.Err() != nil {
			err = mailContextError{ctx.Err()}
		} else if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			err = mailContextError{context.DeadlineExceeded}
		}
	}()
	select {
	case smtpSlots <- struct{}{}:
		defer func() { <-smtpSlots }()
	case <-ctx.Done():
		return mailContextError{ctx.Err()}
	}
	if ctx.Err() != nil {
		return mailContextError{ctx.Err()}
	}
	if host == "" || port < 1 || port > 65535 {
		return fmt.Errorf("SMTP 主机或端口无效")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("无法连接邮件服务器，请检查主机和端口")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(25 * time.Second)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	_ = conn.SetDeadline(deadline)

	var c *smtp.Client
	if port == 465 {
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("邮件加密握手失败，请检查证书和端口")
		}
		c, err = smtp.NewClient(tc, host)
	} else {
		c, err = smtp.NewClient(conn, host)
	}
	if err != nil {
		return fmt.Errorf("建立邮件会话失败")
	}
	defer c.Close()

	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("邮件加密升级失败，请检查服务器证书")
			}
		}
	}
	if user != "" || pass != "" {
		if err := smtpAuth(c, host, user, pass); err != nil {
			return fmt.Errorf("邮件认证失败，请检查用户名/密码及服务器加密设置")
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("发件地址被拒绝")
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("收件地址被拒绝，请检查地址或发送权限")
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("邮件服务器拒绝接收内容")
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("写入邮件内容失败")
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("邮件投递未获服务器确认")
	}
	return nil
}

// SendMail 发送纯文本邮件（自动走多账号轮流负载/失败切换）。
func (n *Notifier) SendMail(ctx context.Context, to, subject, body string) error {
	return n.sendMailFormat(ctx, to, subject, body, mailFormatText)
}

func (n *Notifier) sendMailFormat(ctx context.Context, to, subject, body, format string) error {
	ctx, done, err := n.beginMail(ctx, mailSendTimeout)
	if err != nil {
		return err
	}
	defer done()
	return n.sendWithChannels(ctx, to, subject, body, format)
}

type mailContextError struct{ cause error }

func (e mailContextError) Error() string {
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "邮件发送超时，请稍后重试"
	}
	return "邮件发送已取消"
}
func (e mailContextError) Unwrap() error { return e.cause }

func (n *Notifier) beginMail(ctx context.Context, timeout time.Duration) (context.Context, func(), error) {
	if n == nil {
		return nil, nil, fmt.Errorf("邮件服务不可用")
	}
	n.initMail()
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return nil, nil, fmt.Errorf("邮件服务正在停止")
	}
	n.workers.Add(1)
	n.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	stop := context.AfterFunc(n.mailCtx, cancel)
	return ctx, func() { stop(); cancel(); n.workers.Done() }, nil
}

// sendWithChannels 多账号轮流负载发送（调用时自行加载账号配置）。
func (n *Notifier) sendWithChannels(ctx context.Context, to, subject, body string, formats ...string) error {
	if n == nil || n.db == nil || n.Settings == nil {
		return fmt.Errorf("邮件服务不可用")
	}
	return n.sendWithAccounts(ctx, n.loadAccounts(ctx), to, subject, body, formats...)
}

// sendWithAccounts 多账号轮流负载发送：
//   - 每封从“上次成功账号的下一个”开始（round robin）；
//   - 发送失败的账号进入冷却，本次自动尝试下一个可用账号；
//   - 冷却中的账号跳过；全部失败返回汇总错误。
//   - accounts 由调用方加载传入，避免同一请求内重复读库。
func (n *Notifier) sendWithAccounts(ctx context.Context, accounts []MailAccount, to, subject, body string, formats ...string) error {
	if err := validateMailRecipient(to); err != nil {
		return err
	}
	if err := validateMailContent(subject, body, mailFormat(formats)); err != nil {
		return err
	}
	if len(accounts) == 0 {
		return fmt.Errorf("尚未配置 SMTP 账号，请先在「系统设置-邮件服务」填写并保存")
	}
	if ctx.Err() != nil {
		return mailContextError{ctx.Err()}
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
		if err := n.sendAccount(ctx, accounts, idx, to, subject, body, false, formats...); err != nil {
			if ctx.Err() != nil {
				return mailContextError{ctx.Err()}
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
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
// accounts 由调用方加载传入：sendWithAccounts 的失败重试循环与 Notify 均可能连续调用本函数，
// 若在此重复读库会把账号查询放大到每账号一次。
func (n *Notifier) sendAccount(ctx context.Context, accounts []MailAccount, idx int, to, subject, body string, ignoreEnabled bool, formats ...string) error {
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
	for _, address := range []string{from, to} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || strings.ContainsAny(address, "\r\n") {
			return fmt.Errorf("发件或收件邮箱格式无效")
		}
	}
	if err := validateMailContent(subject, body, mailFormat(formats)); err != nil {
		return err
	}
	site := n.SiteName(ctx)
	if !emailHeaderValid(site) || len(site) > maxMailSubject {
		return fmt.Errorf("邮件站点名称格式无效")
	}
	msg := buildMailMessage(mailHeaderFrom(site, from), to, subject, body, formats...)
	if err := smtpDeliver(ctx, host, acct.Port, acct.User, acct.Pass, from, to, msg); err != nil {
		return fmt.Errorf("账号 #%d「%s」发送失败: %w", idx+1, acct.accountName(), err)
	}
	return nil
}

// runSendTest 统一带 15s 超时执行测试发送：idx<0 走整链路轮流负载，否则定向测试该账号（无视冷却与启用开关）。
func (n *Notifier) runSendTest(ctx context.Context, idx int, to, subject, body string) error {
	if n == nil || n.db == nil || n.Settings == nil {
		return fmt.Errorf("邮件服务不可用")
	}
	ctx, done, err := n.beginMail(ctx, 15*time.Second)
	if err != nil {
		return err
	}
	defer done()
	if idx >= 0 {
		return n.sendAccount(ctx, n.loadAccounts(ctx), idx, to, subject, body, true)
	}
	return n.sendWithChannels(ctx, to, subject, body)
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

func (n *Notifier) MarkOneRead(ctx context.Context, userID, id int64) error {
	_, err := n.db.ExecContext(ctx, `UPDATE notifications SET read=true WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

func (n *Notifier) DeleteOne(ctx context.Context, userID, id int64) error {
	_, err := n.db.ExecContext(ctx, `DELETE FROM notifications WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

func (n *Notifier) DeleteAll(ctx context.Context, userID int64) error {
	_, err := n.db.ExecContext(ctx, `DELETE FROM notifications WHERE user_id=$1`, userID)
	return err
}

// UserNotifications 取用户站内信（最新在前）。
func (n *Notifier) List(ctx context.Context, userID int64) ([]map[string]any, error) {
	list, _, _, err := n.ListPage(ctx, userID, "", "", 1, 200)
	return list, err
}

func (n *Notifier) ListPage(ctx context.Context, userID int64, category, keyword string, page, limit int) ([]map[string]any, int, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}
	where := ` WHERE n.user_id=$1`
	args := []any{userID}
	arg := 2
	if category != "" {
		where += ` AND n.category=$` + strconv.Itoa(arg)
		args = append(args, category)
		arg++
	}
	if keyword != "" {
		where += ` AND (n.title ILIKE $` + strconv.Itoa(arg) + ` OR n.body ILIKE $` + strconv.Itoa(arg) + `)`
		args = append(args, "%"+keyword+"%")
		arg++
	}
	var total, unread int
	if err := n.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications n`+where, args...).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	if err := n.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications n`+where+` AND n.read=false`, args...).Scan(&unread); err != nil {
		return nil, 0, 0, err
	}
	args = append(args, limit, (page-1)*limit)
	rows, err := n.db.QueryContext(ctx,
		`SELECT n.id,n.title,n.body,n.category,n.read,to_char(n.created_at,'YYYY-MM-DD HH24:MI') FROM notifications n`+where+
			` ORDER BY id DESC LIMIT $`+strconv.Itoa(arg)+` OFFSET $`+strconv.Itoa(arg+1), args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var title, body, category, t string
		var read bool
		if err := rows.Scan(&id, &title, &body, &category, &read, &t); err != nil {
			return nil, 0, 0, err
		}
		out = append(out, map[string]any{"id": id, "title": title, "body": body, "category": category, "read": read, "time": t})
	}
	return out, total, unread, rows.Err()
}
