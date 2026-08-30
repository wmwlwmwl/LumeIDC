package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/smtp"
	"strconv"

	"lumeidc/internal/repo"
)

// Notifier 站内信 + 邮件通知。ponytail: 邮件走标准 net/smtp，未配置 SMTP 时仅写入站内信。
type Notifier struct {
	DB       *sql.DB
	Settings *repo.Settings
}

// SMTPConfig 从 settings 表读取的邮件配置。
type SMTPConfig struct {
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	Enabled bool
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
		Host:    get("smtp_host"),
		Port:    port,
		User:    get("smtp_user"),
		Pass:    get("smtp_pass"),
		From:    get("smtp_from"),
		Enabled: get("notify_email_enabled") == "1",
	}
}

// Notify 写站内信；若启用邮件则尝试发送。任何失败仅记日志，不阻断主流程。
func (n *Notifier) Notify(ctx context.Context, userID int64, title, body string) {
	if n == nil || n.DB == nil {
		return
	}
	if _, err := n.DB.ExecContext(ctx,
		`INSERT INTO notifications(user_id,title,body) VALUES($1,$2,$3)`, userID, title, body); err != nil {
		log.Printf("[notify] 站内信写入失败 user=%d: %v", userID, err)
	}
	if n.Settings != nil {
		var email string
		if err := n.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err == nil && email != "" {
			if err := n.sendEmail(ctx, email, title, body); err != nil {
				log.Printf("[notify] 邮件发送失败 user=%d: %v", userID, err)
			}
		}
	}
}

// EmailEnabled 返回是否配置了可用 SMTP。
func (n *Notifier) EmailEnabled(ctx context.Context) bool {
	return n != nil && n.loadSMTP(ctx).Enabled && n.loadSMTP(ctx).Host != ""
}

// SendMail 发送纯文本邮件。
func (n *Notifier) SendMail(ctx context.Context, to, subject, body string) error {
	return n.sendEmail(ctx, to, subject, body)
}

func (n *Notifier) sendEmail(ctx context.Context, to, subject, body string) error {
	cfg := n.loadSMTP(ctx)
	if !cfg.Enabled || cfg.Host == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	msg := []byte("To: " + to + "\r\n" +
		"From: " + cfg.From + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, msg)
}

// MarkNotificationsRead 标记用户全部站内信为已读。
func (n *Notifier) MarkRead(ctx context.Context, userID int64) error {
	_, err := n.DB.ExecContext(ctx, `UPDATE notifications SET read=true WHERE user_id=$1`, userID)
	return err
}

// UserNotifications 取用户站内信（最新在前）。
func (n *Notifier) List(ctx context.Context, userID int64) ([]map[string]any, error) {
	rows, err := n.DB.QueryContext(ctx,
		`SELECT id,title,body,read,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM notifications
		 WHERE user_id=$1 ORDER BY id DESC LIMIT 50`, userID)
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
