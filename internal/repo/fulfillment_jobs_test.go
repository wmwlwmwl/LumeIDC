package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestRetryLaterKeepsJobAlive "一直重试"成立的关键：attempts 必须归零。
// 否则累计到 8 次仍会被 Fail 判 dead，上游充值到账也救不回来。
func TestRetryLaterKeepsJobAlive(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()

	// fulfillment_jobs.service_id 是 NOT NULL 外键，复用库里已有的用户/产品造一条服务。
	var uid, pid int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&uid); err != nil {
		t.Skip("库中暂无用户，跳过")
	}
	if err := d.QueryRowContext(ctx, `SELECT id FROM products LIMIT 1`).Scan(&pid); err != nil {
		t.Skip("库中暂无产品，跳过")
	}
	var sid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status) VALUES($1,$2,0) RETURNING id`, uid, pid).
		Scan(&sid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// 删服务会级联删掉任务。
		if _, err := d.ExecContext(ctx, `DELETE FROM services WHERE id=$1`, sid); err != nil {
			t.Errorf("清理测试服务失败: %v", err)
		}
	}()

	var jobID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,kind,cycle,status,attempts,dedupe_key)
		 VALUES($1,'renew','monthly','running',7,$2) RETURNING id`,
		sid, "repo-test:"+time.Now().Format("150405.000000000")).Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	jobs := &FulfillmentJobs{db: d}
	if err := jobs.RetryLater(ctx, jobID, errors.New("上游余额不足"), 10*time.Minute); err != nil {
		t.Fatalf("RetryLater 失败: %v", err)
	}

	var status string
	var attempts int
	var nextAt time.Time
	if err := d.QueryRowContext(ctx,
		`SELECT status,attempts,next_attempt_at FROM fulfillment_jobs WHERE id=$1`, jobID).
		Scan(&status, &attempts, &nextAt); err != nil {
		t.Fatal(err)
	}
	if status != "retry" {
		t.Fatalf("状态应为 retry，实得 %q", status)
	}
	if attempts != 0 {
		t.Fatalf("attempts 应归零（否则累计到 8 次仍被判 dead），实得 %d", attempts)
	}
	if nextAt.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("下次重试应约在 10 分钟后，实得 %v", nextAt)
	}
}
