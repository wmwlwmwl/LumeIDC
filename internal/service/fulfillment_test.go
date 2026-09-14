package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// setupFulfillment 造一套「服务器 + 产品 + 服务 + 待重试任务」，测试结束自建自删。
func setupFulfillment(t *testing.T, d *sql.DB, retryEnabled bool, retryMinutes int) (jobID, serviceID int64) {
	t.Helper()
	ctx := context.Background()

	var serverID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,retry_later_enabled,retry_later_interval_minutes)
		 VALUES('单元测试-重试策略','fake','http://127.0.0.1:1',$1,$2) RETURNING id`,
		retryEnabled, retryMinutes).Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	var uid int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'测试') RETURNING id`, "fulfillment-policy-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var pid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id) VALUES(NULL,'单元测试-重试策略',-1,$1) RETURNING id`,
		serverID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,server_id,status) VALUES($1,$2,$3,0) RETURNING id`,
		uid, pid, serverID).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,kind,cycle,status,attempts,dedupe_key,lease_until)
		 VALUES($1,'provision','monthly','running',3,$2,now()+interval '3 minutes') RETURNING id`,
		serviceID, "svc-retry-test:"+time.Now().Format("150405.000000000")).Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, s := range []struct {
			q   string
			arg int64
		}{
			{`DELETE FROM services WHERE id=$1`, serviceID}, // 级联删任务
			{`DELETE FROM products WHERE id=$1`, pid},
			{`DELETE FROM servers WHERE id=$1`, serverID},
			{`DELETE FROM users WHERE id=$1`, uid},
		} {
			if _, err := d.ExecContext(ctx, s.q, s.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", s.q, err)
			}
		}
	})
	return jobID, serviceID
}

// jobState 读任务状态、已用重试次数、下次重试时间。
func jobState(t *testing.T, d *sql.DB, jobID int64) (string, int, time.Time) {
	t.Helper()
	var status string
	var attempts int
	var nextAt time.Time
	if err := d.QueryRow(
		`SELECT status,attempts,next_attempt_at FROM fulfillment_jobs WHERE id=$1`, jobID).
		Scan(&status, &attempts, &nextAt); err != nil {
		t.Fatal(err)
	}
	return status, attempts, nextAt
}

// 上游重试策略：关闭后"等外部条件"的失败应立即转人工，而不是继续自动重试。
func TestMarkRetryLaterFollowsServerPolicy(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	jobs := repo.NewFulfillmentJobs(d)
	servers := repo.NewServers(d)
	ff := &Fulfillment{Jobs: jobs, Lifecycle: &Lifecycle{Servers: servers}}
	cause := &server.RetryLaterError{Msg: "账户余额不足"}

	t.Run("开启：保持重试且不消耗重试次数", func(t *testing.T) {
		jobID, serviceID := setupFulfillment(t, d, true, 30)
		if err := ff.markRetryLater(ctx, &repo.FulfillmentJob{ID: jobID, ServiceID: serviceID}, cause); err != nil {
			t.Fatalf("应按策略保持重试: %v", err)
		}
		status, attempts, nextAt := jobState(t, d, jobID)
		if status != "retry" {
			t.Fatalf("状态应为 retry，实得 %q", status)
		}
		if attempts != 0 {
			t.Fatalf("attempts 应归零（否则累计到 8 次仍被判 dead），实得 %d", attempts)
		}
		if nextAt.Before(time.Now().Add(29 * time.Minute)) {
			t.Fatalf("间隔应取服务器配置的 30 分钟，实得 %v", nextAt)
		}
	})

	t.Run("关闭：立即转人工复核", func(t *testing.T) {
		jobID, serviceID := setupFulfillment(t, d, false, 10)
		if err := ff.markRetryLater(ctx, &repo.FulfillmentJob{ID: jobID, ServiceID: serviceID}, cause); err != nil {
			t.Fatalf("应转人工复核: %v", err)
		}
		status, _, _ := jobState(t, d, jobID)
		if status != "manual_review" {
			t.Fatalf("关闭自动重试后应转 manual_review，实得 %q", status)
		}
	})
}

type interruptedRenewProvider struct{ server.Provider }

func (interruptedRenewProvider) Code() string { return "fake-upgrade" }
func (interruptedRenewProvider) Renew(context.Context, server.Config, int64, string, server.CheckpointStore) error {
	return context.DeadlineExceeded
}

func TestFulfillmentInterruptedOperationIsQuarantined(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRenewFixture(t, d)
	if _, err := d.Exec(`UPDATE services SET upstream_provider='fake-upgrade' WHERE id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET status='queued' WHERE service_id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	registry := server.NewRegistry()
	registry.Register(interruptedRenewProvider{})
	jobs := repo.NewFulfillmentJobs(d)
	ff := &Fulfillment{Jobs: jobs, Lifecycle: &Lifecycle{db: d, Providers: registry, Servers: repo.NewServers(d), Provisions: repo.NewProvisionRepo(d)}}
	if worked, err := ff.ProcessOne(ctx); err != nil || !worked {
		t.Fatalf("中断任务应被安全隔离：%v，%v", worked, err)
	}
	var state string
	var recovery bool
	if err := d.QueryRow(`SELECT status,recovery_required FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&state, &recovery); err != nil || state != "manual_review" || !recovery {
		t.Fatalf("超时不能自动重放：%s，%v，%v", state, recovery, err)
	}
}

func TestRecoveredFulfillmentBlocksAdminWrites(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRenewFixture(t, d)
	jobs := repo.NewFulfillmentJobs(d)
	provisions := repo.NewProvisionRepo(d)
	pay := &Payment{db: d, Jobs: jobs, Provisions: provisions}
	if err := provisions.SetCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice, "保留原账单"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET status='running',lease_until=now()-interval '1 second' WHERE service_id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	if job, err := jobs.Claim(ctx, time.Minute); err != nil || job != nil {
		t.Fatalf("恢复只应隔离：%+v，%v", job, err)
	}
	for name, action := range map[string]func() error{
		"重试续费":  func() error { return pay.RetryRenew(ctx, fx.svcID) },
		"取消续费":  func() error { return pay.RefundRenew(ctx, fx.userID, fx.svcID, "未知结果") },
		"取消升级":  func() error { return pay.RefundUpgrade(ctx, fx.userID, fx.svcID, "未知结果") },
		"取消开通":  func() error { return pay.RefundPendingService(ctx, fx.userID, fx.svcID, "未知结果") },
		"覆写检查点": func() error { return provisions.SetCheckpoint(ctx, fx.svcID, renewDoneCkKey(fx.orderID), "refunded") },
		"删除检查点": func() error { return provisions.DeleteCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice) },
	} {
		if err := action(); err == nil {
			t.Fatalf("%s不能绕过未知结果隔离", name)
		}
	}
	if v, ok, err := provisions.GetCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice); err != nil || !ok || v != "保留原账单" {
		t.Fatalf("不能丢失账单证据：%q，%v，%v", v, ok, err)
	}
	if _, ok, err := provisions.GetCheckpoint(ctx, fx.svcID, renewPriceOkKey(fx.orderID)); err != nil || ok {
		t.Fatalf("拒绝重试不能写入价格确认：%v，%v", ok, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("不能添加额外任务：%d，%v", count, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM refunds WHERE order_id=$1`, fx.orderID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("隔离任务不能自动进入退款处置：%d，%v", count, err)
	}
}

// 查不到服务器（本地服务/服务不存在）时用默认策略，而不是报错——否则失败处理本身会挂。
func TestRetryLaterPolicyDefaults(t *testing.T) {
	d := testDB(t)
	enabled, minutes, err := repo.NewServers(d).RetryLaterPolicy(context.Background(), -1)
	if err != nil {
		t.Fatalf("查不到应返回默认值而不是错误: %v", err)
	}
	if !enabled || minutes != repo.DefaultRetryLaterMinutes {
		t.Fatalf("默认应为 启用/%d 分钟，实得 %v/%d", repo.DefaultRetryLaterMinutes, enabled, minutes)
	}
}
