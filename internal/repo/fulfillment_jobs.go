package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type FulfillmentJob struct {
	ID        int64
	ServiceID int64
	OrderID   sql.NullInt64
	Kind      string
	Cycle     string
	Attempts  int
}

type FulfillmentJobs struct{ db *sql.DB }

func (r *FulfillmentJobs) EnqueueTx(ctx context.Context, tx *sql.Tx, serviceID, orderID int64, kind, cycle string) error {
	key := fmt.Sprintf("%s:%d", kind, orderID)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,dedupe_key)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT (dedupe_key) DO NOTHING`,
		serviceID, orderID, kind, cycle, key)
	return err
}

func (r *FulfillmentJobs) Claim(ctx context.Context, lease time.Duration) (*FulfillmentJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var j FulfillmentJob
	err = tx.QueryRowContext(ctx,
		`SELECT id,service_id,order_id,kind,cycle,attempts
		 FROM fulfillment_jobs
		 WHERE status IN ('queued','retry') AND next_attempt_at<=now()
		   AND (lease_until IS NULL OR lease_until<now())
		 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).
		Scan(&j.ID, &j.ServiceID, &j.OrderID, &j.Kind, &j.Cycle, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='running',attempts=attempts+1,
		 lease_until=now()+($2 * interval '1 second'),updated_at=now() WHERE id=$1`,
		j.ID, int64(lease.Seconds()))
	if err != nil {
		return nil, err
	}
	j.Attempts++
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *FulfillmentJobs) Complete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='succeeded',lease_until=NULL,last_error='',updated_at=now() WHERE id=$1`, id)
	return err
}

// HasRunningJob 检查指定服务是否有正在执行的履约任务。
func (r *FulfillmentJobs) HasRunningJob(ctx context.Context, serviceID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=$1 AND status='running')`,
		serviceID).Scan(&exists)
	return exists, err
}

// EnqueueRetry 入队重试任务（管理员手动重试），dedupe key 含时间戳避免与已有任务冲突。
// orderID 为 0 表示任务不依赖订单（开通）；升级必须带订单号，否则定位不到升级单与检查点。
func (r *FulfillmentJobs) EnqueueRetry(ctx context.Context, serviceID, orderID int64, kind, cycle string) error {
	key := fmt.Sprintf("retry:%s:%d:%d", kind, serviceID, time.Now().UnixNano())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,dedupe_key)
		 VALUES($1,nullif($2,0),$3,$4,$5)`,
		serviceID, orderID, kind, cycle, key)
	return err
}

func (r *FulfillmentJobs) Fail(ctx context.Context, id int64, attempts int, cause error) error {
	status := "retry"
	if attempts >= 8 {
		status = "dead"
	}
	shift := attempts
	if shift > 10 {
		shift = 10
	}
	delay := time.Duration(1<<shift) * time.Minute
	msg := "未知错误"
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status=$2,lease_until=NULL,last_error=$3,
		 next_attempt_at=now()+($4 * interval '1 second'),updated_at=now() WHERE id=$1`,
		id, status, msg, int64(delay.Seconds()))
	return err
}

// RetryLater 保持任务可重试且不消耗重试次数：用于"等上游充值"这类会自愈的失败。
// attempts 归零，避免累计到 8 次被判 dead——上游充值到账后下一次重试即可成功。
// ponytail: 代价是这类失败永不放弃。上游若长期欠费，任务会以 interval 为周期一直留在队列里；
// 账单被删除等真正无解的失败会转 ManualReviewError，不再走这条路径。
func (r *FulfillmentJobs) RetryLater(ctx context.Context, id int64, cause error, interval time.Duration) error {
	msg := "等待外部条件"
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='retry',attempts=0,lease_until=NULL,last_error=$2,
		 next_attempt_at=now()+($3 * interval '1 second'),updated_at=now() WHERE id=$1`,
		id, msg, int64(interval.Seconds()))
	return err
}

// MarkManualReview 标记任务为人工复核状态（settle 成功但 checkpoint 未落库等未知窗口）。
func (r *FulfillmentJobs) MarkManualReview(ctx context.Context, id int64, cause error) error {
	msg := "需要人工复核"
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='manual_review',lease_until=NULL,last_error=$2,updated_at=now() WHERE id=$1`,
		id, msg)
	return err
}
