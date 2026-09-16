package cron

import (
	"context"
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
	for _, name := range []string{"020_notifications.sql", "045_notifications_enhance.sql", "060_mail_outbox.sql", "064_email_templates.sql"} {
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
