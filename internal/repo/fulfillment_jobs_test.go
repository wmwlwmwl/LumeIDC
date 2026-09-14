package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func fulfillmentTestDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var uid, pid, sid int64
	if err := d.QueryRow(`INSERT INTO users(email,password_hash) VALUES($1,'测试') RETURNING id`, "fulfillment-"+rand.Text()+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM users WHERE id=$1`, uid) })
	if err := d.QueryRow(`INSERT INTO products(name,stock) VALUES('履约恢复测试',-1) RETURNING id`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM products WHERE id=$1`, pid) })
	if err := d.QueryRow(`INSERT INTO services(user_id,product_id,status) VALUES($1,$2,0) RETURNING id`, uid, pid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM services WHERE id=$1`, sid) })
	return d, sid
}

func insertFulfillmentJob(t *testing.T, d *sql.DB, sid int64, status string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`INSERT INTO fulfillment_jobs(service_id,kind,cycle,status,dedupe_key)
		VALUES($1,'provision','monthly',$2,$3) RETURNING id`, sid, status, "履约测试:"+rand.Text()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestExpiredFulfillmentIsQuarantined(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	jobs := NewFulfillmentJobs(d)
	id := insertFulfillmentJob(t, d, sid, "running")
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Claim(ctx, 3*time.Minute)
	if err != nil || job != nil {
		t.Fatalf("过期任务不可重新执行：%+v，%v", job, err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM fulfillment_jobs WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "manual_review" {
		t.Fatalf("崩溃任务应进入人工复核，实际为 %s", status)
	}
	if running, err := jobs.HasRunningJob(ctx, sid); err != nil || running {
		t.Fatalf("不能永久保持运行中：%v，%v", running, err)
	}
	if err := jobs.EnqueueRetry(ctx, sid, 0, "provision", "monthly"); err == nil {
		t.Fatal("未知上游结果不能通过人工重试绕过")
	}
}

func TestLateFulfillmentCompleteRejected(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	jobs := NewFulfillmentJobs(d)
	id := insertFulfillmentJob(t, d, sid, "queued")
	job, err := jobs.Claim(ctx, 3*time.Minute)
	if err != nil || job == nil || job.ID != id {
		t.Fatalf("领取失败：%+v，%v", job, err)
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET status='manual_review',lease_until=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Complete(ctx, job); err == nil {
		t.Fatal("迟到的完成回写不能覆盖人工复核")
	}
}

func TestFulfillmentOwnershipAndServiceBarrier(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	jobs := NewFulfillmentJobs(d)
	id := insertFulfillmentJob(t, d, sid, "queued")
	old, err := jobs.Claim(ctx, time.Minute)
	if err != nil || old == nil || old.ID != id {
		t.Fatalf("领取失败：%+v，%v", old, err)
	}
	insertFulfillmentJob(t, d, sid, "queued")
	if next, err := jobs.Claim(ctx, time.Minute); err != nil || next != nil {
		t.Fatalf("有效租约不能被抢占或并行执行：%+v，%v", next, err)
	}
	if err := jobs.EnqueueRetry(ctx, sid, 0, "provision", "monthly"); err == nil {
		t.Fatal("运行期间不能人工入队")
	}
	if err := jobs.RetryLater(ctx, old, nil, 0); err != nil {
		t.Fatal(err)
	}
	current, err := jobs.Claim(ctx, time.Minute)
	if err != nil || current == nil || current.ID != id || current.ClaimVersion <= old.ClaimVersion || current.Attempts != 1 {
		t.Fatalf("次数归零不能复用领取版本：%+v，%v", current, err)
	}
	writes := map[string]func(*FulfillmentJob) error{
		"完成": func(j *FulfillmentJob) error { return jobs.Complete(ctx, j) },
		"失败": func(j *FulfillmentJob) error { return jobs.Fail(ctx, j, errors.New("测试")) },
		"等待": func(j *FulfillmentJob) error { return jobs.RetryLater(ctx, j, nil, time.Minute) },
		"复核": func(j *FulfillmentJob) error { return jobs.MarkManualReview(ctx, j, nil, true) },
	}
	for name, write := range writes {
		if err := write(old); !errors.Is(err, ErrFulfillmentLeaseLost) {
			t.Fatalf("旧版本%s必须拒绝：%v", name, err)
		}
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for name, write := range writes {
		if err := write(current); !errors.Is(err, ErrFulfillmentLeaseLost) {
			t.Fatalf("过期领取%s必须拒绝：%v", name, err)
		}
	}
	if job, err := jobs.Claim(ctx, time.Minute); err != nil || job != nil {
		t.Fatalf("隔离后同服务排队任务不能被领取：%+v，%v", job, err)
	}
	for name, write := range writes {
		if err := write(current); !errors.Is(err, ErrFulfillmentLeaseLost) {
			t.Fatalf("隔离后%s必须拒绝：%v", name, err)
		}
	}
	provisions := NewProvisionRepo(d)
	if err := provisions.SetCheckpoint(ctx, sid, "invoice", "未知账单"); err == nil {
		t.Fatal("隔离后不能覆盖检查点")
	}
	if _, err := d.Exec(`UPDATE services SET status=1,provision_error='' WHERE id=$1`, sid); err == nil {
		t.Fatal("隔离后不能伪装开通完成或清除复核提示")
	}
	var message string
	if err := d.QueryRow(`SELECT provision_error FROM services WHERE id=$1`, sid).Scan(&message); err != nil || message != ErrFulfillmentRecoveryRequired.Error() {
		t.Fatalf("隔离提示必须保留：%s，%v", message, err)
	}
}

func TestFulfillmentConcurrentClaimAndRecovery(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	jobs := NewFulfillmentJobs(d)
	for i := 0; i < 4; i++ {
		insertFulfillmentJob(t, d, sid, "queued")
	}
	type result struct {
		job *FulfillmentJob
		err error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			job, err := jobs.Claim(ctx, time.Minute)
			results <- result{job, err}
		}()
	}
	close(start)
	var claimed *FulfillmentJob
	for i := 0; i < cap(results); i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.job != nil {
			if claimed != nil {
				t.Fatal("同服务并发领取了多个任务")
			}
			claimed = r.job
		}
	}
	if claimed == nil {
		t.Fatal("应至少领取一个任务")
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(results); i++ {
		go func() {
			job, err := jobs.Claim(ctx, time.Minute)
			results <- result{job, err}
		}()
	}
	for i := 0; i < cap(results); i++ {
		r := <-results
		if r.err != nil || r.job != nil {
			t.Fatalf("并发恢复不得重新执行：%+v，%v", r.job, r.err)
		}
	}
	if err := jobs.EnqueueRetry(ctx, sid, 0, "provision", "monthly"); !errors.Is(err, ErrFulfillmentRecoveryRequired) {
		t.Fatalf("并发恢复后仍须隔离：%v", err)
	}
}

func TestFulfillmentNullLeaseAndKnownReview(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	jobs := NewFulfillmentJobs(d)
	id := insertFulfillmentJob(t, d, sid, "running")
	if job, err := jobs.Claim(ctx, time.Minute); err != nil || job != nil {
		t.Fatalf("空租约应隔离而非执行：%+v，%v", job, err)
	}
	var recovery bool
	if err := d.QueryRow(`SELECT recovery_required FROM fulfillment_jobs WHERE id=$1`, id).Scan(&recovery); err != nil || !recovery {
		t.Fatalf("历史空租约必须标记未知：%v，%v", recovery, err)
	}
	// 另一个服务的明确价格暂停仍可人工确认，不受前一个服务隔离影响。
	var other int64
	if err := d.QueryRow(`INSERT INTO services(user_id,product_id,status) SELECT user_id,product_id,0 FROM services WHERE id=$1 RETURNING id`, sid).Scan(&other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM services WHERE id=$1`, other) })
	insertFulfillmentJob(t, d, other, "queued")
	job, err := jobs.Claim(ctx, time.Minute)
	if err != nil || job == nil || job.ServiceID != other {
		t.Fatalf("不同服务不应被隔离阻塞：%+v，%v", job, err)
	}
	if err := jobs.MarkManualReview(ctx, job, errors.New("价格变化"), false); err != nil {
		t.Fatal(err)
	}
	if err := jobs.EnqueueRetry(ctx, other, 0, "provision", "monthly"); err != nil {
		t.Fatalf("明确价格暂停应允许人工继续：%v", err)
	}
	job, err = jobs.Claim(ctx, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("人工确认后应可领取：%+v，%v", job, err)
	}
	if err := jobs.Complete(ctx, job); err != nil {
		t.Fatal(err)
	}
}

func TestRetryLaterKeepsJobAlive(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	id := insertFulfillmentJob(t, d, sid, "queued")
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET attempts=7 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	jobs := NewFulfillmentJobs(d)
	job, err := jobs.Claim(ctx, 3*time.Minute)
	if err != nil || job == nil || job.ID != id {
		t.Fatalf("领取失败：%+v，%v", job, err)
	}
	if err := jobs.RetryLater(ctx, job, errors.New("上游余额不足"), 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	var nextAt time.Time
	if err := d.QueryRow(`SELECT status,attempts,next_attempt_at FROM fulfillment_jobs WHERE id=$1`, id).Scan(&status, &attempts, &nextAt); err != nil {
		t.Fatal(err)
	}
	if status != "retry" || attempts != 0 || nextAt.Before(time.Now().Add(9*time.Minute)) {
		t.Fatalf("等待重试必须保留任务、不消耗次数且遵守间隔：%s/%d/%v", status, attempts, nextAt)
	}
}
