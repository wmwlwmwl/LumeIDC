package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"lumeidc/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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

func TestFulfillmentRetryRejectsUnstoppedHistory(t *testing.T) {
	for _, status := range []string{"queued", "running", "retry", "manual_review", "dead"} {
		t.Run(status, func(t *testing.T) {
			d, sid := fulfillmentTestDB(t)
			id := insertFulfillmentJob(t, d, sid, status)
			if status == "manual_review" || status == "dead" {
				if _, err := d.Exec(`UPDATE fulfillment_jobs SET recovery_required=true WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := NewFulfillmentJobs(d).EnqueueRetry(context.Background(), sid, 0, "provision", "monthly"); err == nil {
				t.Fatal("未停止或未知结果任务不能被接替")
			}
			var count int
			if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE service_id=$1 AND superseded_by IS NULL`, sid).Scan(&count); err != nil || count != 1 {
				t.Fatalf("拒绝必须回滚新任务和历史变更：%d，%v", count, err)
			}
		})
	}
}

func TestFulfillmentSupersededMigrationUpgrade(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置隔离测试数据库")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "fulfillment_migration_" + fmt.Sprint(time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	defer d.Close()
	ctx := context.Background()
	files := db.Migrations()
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, name := range names {
		if name >= "migrations/061_" {
			continue
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}
	if err := db.Migrate(ctx, d, before); err != nil {
		t.Fatal(err)
	}
	var sid, oldID int64
	if err := d.QueryRow(`WITH u AS (INSERT INTO users(email,password_hash) VALUES('迁移测试@example.invalid','测试') RETURNING id),
 p AS (INSERT INTO products(name,stock) VALUES('迁移测试',-1) RETURNING id)
 INSERT INTO services(user_id,product_id,status) SELECT u.id,p.id,0 FROM u,p RETURNING id`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	oldID = insertFulfillmentJob(t, d, sid, "manual_review")
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET last_error='迁移前失败证据' WHERE id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx, d, files); err != nil {
			t.Fatal(err)
		}
	}
	var status, message string
	var by sql.NullInt64
	if err := d.QueryRow(`SELECT status,last_error,superseded_by FROM fulfillment_jobs WHERE id=$1`, oldID).Scan(&status, &message, &by); err != nil {
		t.Fatal(err)
	}
	if status != "manual_review" || message != "迁移前失败证据" || by.Valid {
		t.Fatal("迁移不能无证据批量忽略历史失败")
	}
	if err := NewFulfillmentJobs(d).EnqueueRetry(ctx, sid, 0, "provision", "monthly"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT superseded_by FROM fulfillment_jobs WHERE id=$1`, oldID).Scan(&by); err != nil || !by.Valid {
		t.Fatalf("升级后的历史必须可结构化接替：%v，%v", by, err)
	}
	if _, err := d.Exec(`DELETE FROM services WHERE id=$1`, sid); err != nil {
		t.Fatalf("升级后的历史整链清理失败：%v", err)
	}
}

func TestFulfillmentSupersededMigrationConstraintsAndCleanup(t *testing.T) {
	d, sid := fulfillmentTestDB(t)
	ctx := context.Background()
	a := insertFulfillmentJob(t, d, sid, "dead")
	b := insertFulfillmentJob(t, d, sid, "queued")
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET dedupe_key=$2 WHERE id=$1`, b, fmt.Sprintf("retry:provision:%d:1", sid)); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"自引用":     `UPDATE fulfillment_jobs SET superseded_by=id WHERE id=$1`,
		"不存在的接替者": `UPDATE fulfillment_jobs SET superseded_by=9223372036854775807 WHERE id=$1`,
		"排队任务":    `UPDATE fulfillment_jobs SET superseded_by=$2,status='queued' WHERE id=$1`,
		"运行任务":    `UPDATE fulfillment_jobs SET superseded_by=$2,status='running' WHERE id=$1`,
		"隔离任务":    `UPDATE fulfillment_jobs SET superseded_by=$2,recovery_required=true WHERE id=$1`,
		"残留租约":    `UPDATE fulfillment_jobs SET superseded_by=$2,lease_until=now() WHERE id=$1`,
	} {
		t.Run(name, func(t *testing.T) {
			args := []any{a}
			if name != "自引用" && name != "不存在的接替者" {
				args = append(args, b)
			}
			if _, err := d.Exec(query, args...); err == nil {
				t.Fatal("非法接替关系必须被数据库拒绝")
			}
		})
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET superseded_by=$2,last_error='原始失败证据' WHERE id=$1`, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM fulfillment_jobs WHERE id=$1`, b); err == nil {
		t.Fatal("不能单删接替者导致审计关系丢失")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if count, err := PendingFulfillmentJobsTx(ctx, tx, sid); err != nil || count != 1 {
		t.Fatalf("结构化关系应消除历史阻断：%d，%v", count, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fulfillment_jobs SET dedupe_key='非人工重试' WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if count, err := PendingFulfillmentJobsTx(ctx, tx, sid); err != nil || count != 2 {
		t.Fatalf("不能信任业务不匹配的结构化关系：%d，%v", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM services WHERE id=$1`, sid); err != nil {
		t.Fatalf("服务级联清理不能被自引用外键阻断：%v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE id IN ($1,$2)`, a, b).Scan(&count); err != nil || count != 0 {
		t.Fatalf("整链清理失败：%d，%v", count, err)
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
