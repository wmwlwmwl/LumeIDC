package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	database "lumeidc/internal/db"
	"lumeidc/internal/repo"
)

func mailTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库，跳过邮件队列集成测试")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	if !strings.Contains(strings.ToLower(cfg.Database), "test") || (cfg.Host != "localhost" && !net.ParseIP(cfg.Host).IsLoopback()) {
		t.Fatal("邮件测试仅允许本机且库名包含 test 的隔离数据库")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("mail_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		_ = d.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	// 先迁移旧版本并插入历史通知，再应用 060，确保不会补发历史。
	old := fstest.MapFS{}
	names, err := fs.Glob(database.Migrations(), "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "migrations/060_" {
			continue
		}
		b, err := fs.ReadFile(database.Migrations(), name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: b}
	}
	if err := database.Migrate(context.Background(), d, old); err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `INSERT INTO notifications(user_id,title,body) VALUES(1,'历史通知','不补发')`)
	if err := database.Migrate(context.Background(), d, database.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), d, database.Migrations()); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("迁移补发了历史通知")
	}
	mailExec(t, d, `DELETE FROM notifications`)
	return d
}

func mailExec(t *testing.T, d *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func mailCount(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func mailTestNotifier(t *testing.T, d *sql.DB) (*Notifier, int64) {
	t.Helper()
	var id int64
	if err := d.QueryRow(`INSERT INTO users(email,password_hash) VALUES('to@x.test','测试') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(d, repo.NewSettings(d))
	if err := n.Settings.Set(context.Background(), "notify_email_forward_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = n.StopMail(ctx)
	})
	return n, id
}

func TestMailOutboxPersistenceLeaseAndDelete(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	ctx := context.Background()
	if err := n.Notify(ctx, uid, "服务提醒", "正文"); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "notifications") != 1 || mailCount(t, d, "mail_outbox") != 1 {
		t.Fatal("未一起保存站内信和邮件意图")
	}
	if err := n.DeleteAll(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "mail_outbox") != 1 {
		t.Fatal("删除站内信丢失待发邮件")
	}
	// 用新实例模拟重启；发送凭据没有持久化在任务中。
	n = NewNotifier(d, repo.NewSettings(d))
	job, err := n.claimMail(ctx)
	if err != nil || job.to != "to@x.test" || !strings.Contains(job.subject, DefaultSiteName) {
		t.Fatalf("重启领取失败：%+v %v", job, err)
	}
	if _, err := n.claimMail(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("活跃租约被重复领取：%v", err)
	}
	if err := n.finishMail(ctx, job, false); err != nil {
		t.Fatal(err)
	}
	if _, err := n.claimMail(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("退避未生效：%v", err)
	}
	mailExec(t, d, `UPDATE mail_outbox SET available_at=now()-interval '1 second'`)
	job, err = n.claimMail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `UPDATE mail_outbox SET lease_until=now()-interval '1 second'`)
	newJob, err := n.claimMail(ctx)
	if err != nil || newJob.version <= job.version {
		t.Fatalf("过期租约未回收：%v", err)
	}
	if err := n.finishMail(ctx, job, true); err != nil {
		t.Fatal(err)
	}
	if err := n.finishMail(ctx, job, false); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM mail_outbox`).Scan(&state); err != nil || state != "running" {
		t.Fatal("旧版本写回覆盖了新租约")
	}
	if err := n.finishMail(ctx, newJob, true); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("成功任务未清理")
	}
}

func TestMailOutboxFailedDeliveryPersists(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	srv := startFakeSMTP(t, "LOGIN", true)
	host, portText, _ := net.SplitHostPort(srv.addr())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	accounts, _ := json.Marshal([]MailAccount{{Enabled: true, Host: host, Port: port, User: "用户", Pass: "仅设置中", From: "from@x.test"}})
	if err := n.Settings.Set(context.Background(), keySMTPAccounts, string(accounts)); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), uid, "提醒", "正文"); err != nil {
		t.Fatal(err)
	}
	n.StartMail()
	waitMail(t, func() bool {
		var retried bool
		err := d.QueryRow(`SELECT state='pending' AND attempts=1 AND available_at>now() FROM mail_outbox`).Scan(&retried)
		return err == nil && retried
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := n.StopMail(ctx); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := d.QueryRow(`SELECT row_to_json(mail_outbox)::text FROM mail_outbox`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, "仅设置中") {
		t.Fatal("邮件任务泄漏账号密码")
	}
	if mailCount(t, d, "mail_outbox") != 1 {
		t.Fatal("失败投递丢失持久任务")
	}
}

func TestMailOutboxAtomicAndDisabled(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	ctx := context.Background()
	mailExec(t, d, `ALTER TABLE mail_outbox ADD CONSTRAINT reject_test CHECK (subject='不可能的主题')`)
	if err := n.Notify(ctx, uid, "测试", "正文"); err == nil {
		t.Fatal("邮件落库失败未返回错误")
	}
	if mailCount(t, d, "notifications") != 0 {
		t.Fatal("邮件意图失败未回滚站内信")
	}
	mailExec(t, d, `ALTER TABLE mail_outbox DROP CONSTRAINT reject_test`)
	mailExec(t, d, `ALTER TABLE settings RENAME TO settings_unavailable`)
	if err := n.Notify(ctx, uid, "测试", "正文"); err == nil {
		t.Fatal("读取设置失败被当作关闭邮件")
	}
	if mailCount(t, d, "notifications") != 0 {
		t.Fatal("设置读取失败未回滚")
	}
	mailExec(t, d, `ALTER TABLE settings_unavailable RENAME TO settings`)
	if err := n.Settings.Set(ctx, "notify_email_forward_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(ctx, uid, "仅站内信", "正文"); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, d, "notifications") != 1 || mailCount(t, d, "mail_outbox") != 0 {
		t.Fatal("关闭转发仍入队邮件")
	}
}

func TestMailOutboxWorkersReserveSyncAndShutdown(t *testing.T) {
	d := mailTestDB(t)
	n, uid := mailTestNotifier(t, d)
	host, port, active, peak := startStalledSMTP(t)
	accounts, _ := json.Marshal([]MailAccount{{Enabled: true, Host: host, Port: port, From: "from@x.test"}})
	if err := n.Settings.Set(context.Background(), keySMTPAccounts, string(accounts)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := n.Notify(context.Background(), uid, "提醒", "正文"); err != nil {
			t.Fatal(err)
		}
	}
	n.StartMail()
	n.StartMail()
	waitMail(t, func() bool { return active.Load() == mailWorkers })
	results := make(chan error, 2)
	go func() { results <- n.SendMail(context.Background(), "to@x.test", "验证码", "仅内存") }()
	go func() { results <- n.SendTestMailAccount(context.Background(), 0, "to@x.test", "测试", "仅内存") }()
	waitMail(t, func() bool { return active.Load() == mailConnections })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := n.StopMail(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("同步发送未取消：%v", err)
		}
	}
	waitMail(t, func() bool { return active.Load() == 0 })
	if peak.Load() > mailConnections || len(n.cooldownUntil) != 0 {
		t.Fatal("超额并发或停机导致错误冷却")
	}
	if mailCount(t, d, "mail_outbox") != 8 {
		t.Fatal("取消丢失任务或同步邮件错误入队")
	}
	var running int
	if err := d.QueryRow(`SELECT count(*) FROM mail_outbox WHERE state='running'`).Scan(&running); err != nil || running != 0 {
		t.Fatal("停机未及时归还任务")
	}
	// 新实例继续发送持久任务，使用本机假 SMTP，不向真实邮箱发送。
	srv := startFakeSMTP(t, "", false)
	host, portText, _ := net.SplitHostPort(srv.addr())
	_, _ = fmt.Sscanf(portText, "%d", &port)
	accounts, _ = json.Marshal([]MailAccount{{Enabled: true, Host: host, Port: port, From: "from@x.test"}})
	if err := n.Settings.Set(context.Background(), keySMTPAccounts, string(accounts)); err != nil {
		t.Fatal(err)
	}
	mailExec(t, d, `UPDATE mail_outbox SET available_at=now()`)
	restarted := NewNotifier(d, repo.NewSettings(d))
	restarted.StartMail()
	t.Cleanup(func() { _ = restarted.StopMail(context.Background()) })
	waitMail(t, func() bool { return mailCount(t, d, "mail_outbox") == 0 })
}
