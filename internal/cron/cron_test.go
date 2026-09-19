package cron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"lumeidc/internal/db"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// alertTaskDB 起一个独立 schema 并跑全量迁移（模式同 service/admin_notify_test.go）。
func alertTaskDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库，跳过定时任务告警测试")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	if !strings.Contains(strings.ToLower(cfg.Database), "test") || (cfg.Host != "localhost" && !net.ParseIP(cfg.Host).IsLoopback()) {
		t.Fatal("仅允许本机测试数据库")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("cron_alert_test_%d", time.Now().UnixNano())
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
	if err := db.Migrate(context.Background(), d, db.Migrations()); err != nil {
		t.Fatal(err)
	}
	return d
}

// alertJobs 配好「管理员告警可用」的 Jobs（邮件总开关开 + 收件人已设）。
func alertJobs(t *testing.T, d *sql.DB) Jobs {
	t.Helper()
	ctx := context.Background()
	n := service.NewNotifier(d, repo.NewSettings(d))
	if err := n.SetEmailForwardEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := n.SetAdminNotifyEmail(ctx, "ops@x.test"); err != nil {
		t.Fatal(err)
	}
	return Jobs{DB: d, Notifier: n}
}

// TestAlertTaskFailureNotifiesAdminOncePerDay 覆盖「定时任务级失败必须给管理员告警」：
// 到期停机/删除、补单、账单过期退款这些任务没有用户可见入口，失败只写一行日志
// 等于静默停摆，管理员往往要等到用户投诉才发现。
func TestAlertTaskFailureNotifiesAdminOncePerDay(t *testing.T) {
	d := alertTaskDB(t)
	ctx := context.Background()
	j := alertJobs(t, d)

	// 未注入通知服务时必须安全跳过（cron 在无邮件配置的环境下也要能跑）。
	(&Jobs{DB: d}).alertTaskFailure(ctx, "unit", "无通知服务", errors.New("x"))

	j.alertTaskFailure(ctx, taskExpireInvoices, "开启过期账单事务", errors.New("连接中断"))
	// 同一任务同一天再次失败（含换一个失败阶段）只发一封：该任务每 10 分钟一轮，
	// 不按天去重会把管理员邮箱刷爆。
	j.alertTaskFailure(ctx, taskExpireInvoices, "提交过期账单事务", errors.New("提交超时"))

	count := func(q string) int {
		t.Helper()
		var c int
		if err := d.QueryRow(q).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if got := count(`SELECT count(*) FROM admin_alert_log`); got != 1 {
		t.Fatalf("同一任务当天应只记一条告警，实得 %d", got)
	}
	if got := count(`SELECT count(*) FROM mail_outbox`); got != 1 {
		t.Fatalf("同一任务当天应只发一封告警邮件，实得 %d", got)
	}
	var key, subject, body string
	if err := d.QueryRow(`SELECT alert_key,subject,body FROM admin_alert_log`).Scan(&key, &subject, &body); err != nil {
		t.Fatal(err)
	}
	if want := "cron:" + taskExpireInvoices + ":" + time.Now().Format("2006-01-02"); key != want {
		t.Fatalf("去重键应含任务与当天日期：期望 %q，实得 %q", want, key)
	}
	if !strings.Contains(subject, "开启过期账单事务失败") || !strings.Contains(body, "连接中断") {
		t.Fatalf("告警应带上首次失败的任务与原因：subject=%q body=%q", subject, body)
	}

	// 不同任务各自告警，不能互相顶掉。
	j.alertTaskFailure(ctx, taskReleaseStock, "释放过期库存预留", errors.New("锁等待超时"))
	if got := count(`SELECT count(*) FROM admin_alert_log`); got != 2 {
		t.Fatalf("不同任务应各自告警，实得 %d", got)
	}
	// err 为 nil 不是失败，不该告警。
	j.alertTaskFailure(ctx, "unit", "没有失败", nil)
	if got := count(`SELECT count(*) FROM admin_alert_log`); got != 2 {
		t.Fatalf("nil 错误不应告警，实得 %d", got)
	}
}

// TestTaskFailureWiringAlertsAdmin 端到端验证接线：真实任务失败必须真的产出告警
// （只测 alertTaskFailure 本身无法证明各任务把它调上了）。
func TestTaskFailureWiringAlertsAdmin(t *testing.T) {
	d := alertTaskDB(t)
	ctx := context.Background()
	j := alertJobs(t, d)
	// 抽掉任务依赖的表，制造一次真实的任务级失败。
	if _, err := d.Exec(`DROP TABLE stock_reservations CASCADE`); err != nil {
		t.Fatal(err)
	}
	j.releaseExpiredStock(ctx)

	var key, body string
	if err := d.QueryRow(`SELECT alert_key,body FROM admin_alert_log`).Scan(&key, &body); err != nil {
		t.Fatalf("任务失败必须落到管理员告警：%v", err)
	}
	if want := "cron:" + taskReleaseStock + ":" + time.Now().Format("2006-01-02"); key != want {
		t.Fatalf("告警键不符：期望 %q，实得 %q", want, key)
	}
	if !strings.Contains(body, "释放过期库存预留") || !strings.Contains(body, "stock_reservations") {
		t.Fatalf("告警正文应指明失败的任务阶段与原因：%q", body)
	}
}

func TestReminderMarksOnlyAfterEnqueue(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	if !strings.Contains(strings.ToLower(cfg.Database), "test") || (cfg.Host != "localhost" && !net.ParseIP(cfg.Host).IsLoopback()) {
		t.Fatal("仅允许本机测试数据库")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("mail_cron_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = d.Close(); _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = admin.Close() })
	exec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE users(id bigint PRIMARY KEY,email text);
	CREATE TABLE settings(key text PRIMARY KEY,value text);
	CREATE TABLE tickets(id bigint,user_id bigint,subject text,status text,updated_at timestamptz,timeout_notified_at timestamptz);
	CREATE TABLE services(id bigint,user_id bigint,status int,expires_at timestamptz,expire_warn_sent boolean);
	INSERT INTO users VALUES(1,'to@x.test');
	INSERT INTO settings VALUES('notify_email_forward_enabled','1');
	INSERT INTO tickets VALUES(1,1,'工单','open',now()-interval '2 days',NULL);
	INSERT INTO services VALUES(1,1,1,now()+interval '1 day',false)`)
	// 必须含短信相关迁移：notify 在同一事务里还要按场景绑定写 sms_outbox，
	// 缺表会让入队整体失败，测出的"标记与入队不一致"是夹具缺表而非业务缺陷。
	for _, name := range []string{"020_notifications.sql", "045_notifications_enhance.sql", "060_mail_outbox.sql", "064_email_templates.sql",
		"065_sms_templates.sql", "066_sms_provider_capabilities.sql", "067_sms_routes.sql"} {
		b, err := fs.ReadFile(db.Migrations(), "migrations/"+name)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
	}
	n := service.NewNotifier(d, repo.NewSettings(d))
	j := Jobs{DB: d, Notifier: n}
	exec(`ALTER TABLE mail_outbox ADD CONSTRAINT reject_test CHECK (subject='不可能的主题')`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j.notifyStaleTickets(ctx)
	j.notifyExpiringSoon(ctx)
	check := func(want bool) {
		t.Helper()
		var ticketMarked, serviceMarked bool
		if err := d.QueryRow(`SELECT timeout_notified_at IS NOT NULL FROM tickets`).Scan(&ticketMarked); err != nil {
			t.Fatal(err)
		}
		if err := d.QueryRow(`SELECT expire_warn_sent FROM services`).Scan(&serviceMarked); err != nil {
			t.Fatal(err)
		}
		if ticketMarked != want || serviceMarked != want {
			t.Fatal("提醒标记与入队结果不一致")
		}
	}
	check(false)
	exec(`ALTER TABLE mail_outbox DROP CONSTRAINT reject_test`)
	j.notifyStaleTickets(ctx)
	j.notifyExpiringSoon(ctx)
	check(true)
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM mail_outbox`).Scan(&count); err != nil || count != 2 {
		t.Fatal("提醒未完整保存到邮件队列")
	}
}

// 账单过期的释放动作必须幂等：抢购名额与优惠码占用只能在本轮真正过期的那一次被回退，
// 否则历史过期账单会被每轮 cron 反复释放，名额与券用量被无限放大。
// 同时覆盖「订单存在已支付账单时不得释放」。
func TestExpireInvoicesReleasesOnce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	if !strings.Contains(strings.ToLower(cfg.Database), "test") || (cfg.Host != "localhost" && !net.ParseIP(cfg.Host).IsLoopback()) {
		t.Fatal("仅允许本机测试数据库")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("expire_cron_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = d.Close(); _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = admin.Close() })
	exec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// 只建 expireInvoices 真正读写的表，避免引入迁移文件之间的依赖。
	exec(`CREATE TABLE users(id bigint PRIMARY KEY, balance numeric(12,2) NOT NULL DEFAULT 0);
	CREATE TABLE balance_logs(id bigint GENERATED ALWAYS AS IDENTITY, user_id bigint, amount numeric(12,2), balance_after numeric(12,2), type text, note text);
	CREATE TABLE orders(id bigint PRIMARY KEY, user_id bigint, promo_product_id bigint);
	CREATE TABLE promotion_quota(promotion_product_id bigint PRIMARY KEY, total int, sold int NOT NULL DEFAULT 0, updated_at timestamptz DEFAULT now());
	CREATE TABLE coupons(id bigint PRIMARY KEY, used_count int NOT NULL DEFAULT 0);
	CREATE TABLE coupon_usages(id bigint GENERATED ALWAYS AS IDENTITY, coupon_id bigint, user_id bigint, order_id bigint, discount numeric(10,2) DEFAULT 0);
	CREATE TABLE invoices(id bigint PRIMARY KEY, no text, user_id bigint, order_id bigint, amount numeric(12,2), status smallint, gateway text DEFAULT '', trade_no text DEFAULT '', credit numeric(12,2) DEFAULT 0, due_at timestamptz);
	CREATE TABLE payment_attempts(id bigint GENERATED ALWAYS AS IDENTITY, invoice_id bigint, status smallint);
	INSERT INTO users VALUES(1, 0);
	-- 名额：真实占用 1 个订单，sold 故意留成 3，用来暴露「每轮重复回退」。
	INSERT INTO promotion_quota VALUES(10, 5, 3, now());
	-- 订单 1：账单过期未支付 → 本轮应释放。
	INSERT INTO orders VALUES(1, 1, 10);
	INSERT INTO invoices VALUES(1, 'INV-1', 1, 1, '10.00', 0, '', '', 0, now() - interval '1 hour');
	-- 订单 2：过期账单之外还有已支付账单 → 不得释放。
	INSERT INTO orders VALUES(2, 1, 10);
	INSERT INTO invoices VALUES(2, 'INV-2', 1, 2, '10.00', 0, '', '', 0, now() - interval '1 hour');
	INSERT INTO invoices VALUES(3, 'INV-3', 1, 2, '10.00', 1, 'balance', '', 0, now() - interval '2 hours');
	INSERT INTO coupons VALUES(20, 3);
	INSERT INTO coupon_usages(coupon_id, user_id, order_id) VALUES(20, 1, 1), (20, 1, 2);`)

	j := Jobs{DB: d, Coupons: repo.NewCoupons(d)}
	ctx := context.Background()
	sold := func() int {
		t.Helper()
		var n int
		if err := d.QueryRow(`SELECT sold FROM promotion_quota WHERE promotion_product_id=10`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	usedCount := func() int {
		t.Helper()
		var n int
		if err := d.QueryRow(`SELECT used_count FROM coupons WHERE id=20`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	j.expireInvoices(ctx)
	if got := sold(); got != 2 {
		t.Fatalf("第一轮只应释放订单 1：sold 3 → 2，实得 %d", got)
	}
	if got := usedCount(); got != 2 {
		t.Fatalf("第一轮只应释放订单 1 的券：used_count 3 → 2，实得 %d", got)
	}
	var usages int
	if err := d.QueryRow(`SELECT count(*) FROM coupon_usages`).Scan(&usages); err != nil {
		t.Fatal(err)
	}
	if usages != 1 {
		t.Fatalf("应保留订单 2 的用券记录，实得 %d 条", usages)
	}

	// 第二轮：没有新的账单过期，任何释放都不应再发生。
	j.expireInvoices(ctx)
	if got := sold(); got != 2 {
		t.Fatalf("重复执行不得再次回退名额，实得 %d", got)
	}
	if got := usedCount(); got != 2 {
		t.Fatalf("重复执行不得再次回退用券次数，实得 %d", got)
	}
}

func TestSplitByPresence(t *testing.T) {
	sorted := func(v []int64) []int64 {
		out := append([]int64(nil), v...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	eq := func(a, b []int64) bool {
		sa, sb := sorted(a), sorted(b)
		if len(sa) != len(sb) {
			return false
		}
		for i := range sa {
			if sa[i] != sb[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name            string
		pids            map[int64]int64 // upstreamPID -> localID
		present         map[int64]bool  // localID
		wantOff, wantOn []int64
	}{
		{"全部命中", map[int64]int64{11: 1, 22: 2}, map[int64]bool{1: true, 2: true}, nil, []int64{1, 2}},
		{"全部缺失", map[int64]int64{11: 1, 22: 2}, map[int64]bool{}, []int64{1, 2}, nil},
		{"部分缺失", map[int64]int64{11: 1, 22: 2, 33: 3}, map[int64]bool{2: true}, []int64{1, 3}, []int64{2}},
		{"空输入", map[int64]int64{}, map[int64]bool{9: true}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			off, on := splitByPresence(c.pids, c.present)
			if !eq(off, c.wantOff) {
				t.Fatalf("停售分组不符：期望 %v，实际 %v", c.wantOff, off)
			}
			if !eq(on, c.wantOn) {
				t.Fatalf("在售分组不符：期望 %v，实际 %v", c.wantOn, on)
			}
		})
	}
}
