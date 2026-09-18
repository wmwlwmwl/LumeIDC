package service

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	database "lumeidc/internal/db"
	"lumeidc/internal/repo"
)

// adminAlertDB 起一个独立 schema 跑全量迁移；无 TEST_DATABASE_DSN 时跳过（模式同 mail_outbox_test.go）。
func adminAlertDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库，跳过管理员告警集成测试")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	if !strings.Contains(strings.ToLower(cfg.Database), "test") || (cfg.Host != "localhost" && !net.ParseIP(cfg.Host).IsLoopback()) {
		t.Fatal("管理员告警测试仅允许本机且库名包含 test 的隔离数据库")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("admin_alert_test_%d", time.Now().UnixNano())
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
	if err := database.Migrate(context.Background(), d, database.Migrations()); err != nil {
		t.Fatal(err)
	}
	return d
}

func adminAlertCount(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestNotifyAdminOnceDedupes 同一告警键只发一次：重复调用不新增邮件、不新增去重记录。
func TestNotifyAdminOnceDedupes(t *testing.T) {
	d := adminAlertDB(t)
	ctx := context.Background()
	n := NewNotifier(d, repo.NewSettings(d))
	if err := n.Settings.Set(ctx, keyEmailForwardEnabled, "1"); err != nil {
		t.Fatal(err)
	}
	if err := n.SetAdminNotifyEmail(ctx, "ops@x.test, admin@x.test"); err != nil {
		t.Fatal(err)
	}

	if err := n.NotifyAdminOnce(ctx, "fulfill:provision:1", "fulfillment", "开通失败待处理", "服务的「开通」操作失败。"); err != nil {
		t.Fatal(err)
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 2 {
		t.Fatalf("两位管理员应各收到一封，实得 %d", got)
	}
	if got := adminAlertCount(t, d, "admin_alert_log"); got != 1 {
		t.Fatalf("应只记一条去重记录，实得 %d", got)
	}
	// 同一事件重复触发（重试 / 并发）：不再入队、不再记账。
	for i := 0; i < 3; i++ {
		if err := n.NotifyAdminOnce(ctx, "fulfill:provision:1", "fulfillment", "开通失败待处理", "服务的「开通」操作失败。"); err != nil {
			t.Fatal(err)
		}
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 2 {
		t.Fatalf("同一失败事件重复告警，实得 %d 封", got)
	}
	// 不同事件（人工重试生成的新任务）必须能再发。
	if err := n.NotifyAdminOnce(ctx, "fulfill:retry:provision:1:999", "fulfillment", "开通失败待处理", "服务的「开通」操作失败。"); err != nil {
		t.Fatal(err)
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 4 {
		t.Fatalf("新任务应重新告警，实得 %d 封", got)
	}
}

// TestNotifyAdminSkipsWhenUnconfigured 未配置收件人或业务邮件总开关关闭时不入队，
// 且不消耗去重键——配置补齐后同一事件仍应发出。
func TestNotifyAdminSkipsWhenUnconfigured(t *testing.T) {
	d := adminAlertDB(t)
	ctx := context.Background()
	n := NewNotifier(d, repo.NewSettings(d))

	if err := n.NotifyAdminOnce(ctx, "identity_submit:manual:1", "identity", "新的实名认证申请", "有用户提交了实名认证申请。"); err != nil {
		t.Fatal(err)
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 0 {
		t.Fatalf("未配置收件人不应发信，实得 %d", got)
	}
	if got := adminAlertCount(t, d, "admin_alert_log"); got != 0 {
		t.Fatalf("未送达不应占用去重键，实得 %d", got)
	}

	if err := n.SetAdminNotifyEmail(ctx, "ops@x.test"); err != nil {
		t.Fatal(err)
	}
	if err := n.Settings.Set(ctx, keyEmailForwardEnabled, "0"); err != nil {
		t.Fatal(err)
	}
	if err := n.NotifyAdminOnce(ctx, "identity_submit:manual:1", "identity", "新的实名认证申请", "有用户提交了实名认证申请。"); err != nil {
		t.Fatal(err)
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 0 {
		t.Fatalf("业务邮件总开关关闭时不应发信，实得 %d", got)
	}

	if err := n.Settings.Set(ctx, keyEmailForwardEnabled, "1"); err != nil {
		t.Fatal(err)
	}
	if err := n.NotifyAdminOnce(ctx, "identity_submit:manual:1", "identity", "新的实名认证申请", "有用户提交了实名认证申请。"); err != nil {
		t.Fatal(err)
	}
	if got := adminAlertCount(t, d, "mail_outbox"); got != 1 {
		t.Fatalf("配置补齐后同一事件仍应发出，实得 %d", got)
	}
}

// TestTruncateBytes 标题按字节截断且不切断多字节字符：中文站点名+长标题也必须能过邮件标题校验。
func TestTruncateBytes(t *testing.T) {
	long := strings.Repeat("开", 400)
	got := truncateBytes(long, maxMailSubject)
	if len(got) > maxMailSubject {
		t.Fatalf("截断后仍超 %d 字节：%d", maxMailSubject, len(got))
	}
	if err := validateMailContent(got, "正文", mailFormatHTML); err != nil {
		t.Fatalf("截断后的标题应能通过校验: %v", err)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("超长标题应以省略号结尾")
	}
	if short := truncateBytes("短标题", maxMailSubject); short != "短标题" {
		t.Fatalf("未超限不应改写，实得 %q", short)
	}
}

// distinctAddresses 生成 n 个互不相同的地址，用于验证收件人上限。
func distinctAddresses(n int) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("ops%d@x.test", i))
	}
	return strings.Join(parts, ",")
}

// TestParseAdminNotifyRecipients 收件人解析：多分隔符、去重、丢弃非法地址、限制总数。
func TestParseAdminNotifyRecipients(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"逗号分隔", "a@x.test,b@x.test", 2},
		{"分号与换行", "a@x.test; b@x.test\nc@x.test", 3},
		{"去重大小写", "A@x.test,a@x.test", 1},
		{"丢弃非法", "a@x.test,not-an-email,b@x.test", 2},
		{"拒绝显示名", `a@x.test,"显示名" <b@x.test>`, 1},
		{"空值", "   ", 0},
		{"超出上限", distinctAddresses(MaxAdminAlertRecipients + 5), MaxAdminAlertRecipients},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := len(parseAdminNotifyRecipients(tc.raw))
			if got != tc.want {
				t.Fatalf("解析 %q 得到 %d 个收件人，期望 %d", tc.raw, got, tc.want)
			}
		})
	}
}
