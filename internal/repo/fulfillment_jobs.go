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
func (r *FulfillmentJobs) EnqueueRetry(ctx context.Context, serviceID int64, kind, cycle string) error {
	key := fmt.Sprintf("retry:%s:%d:%d", kind, serviceID, time.Now().UnixNano())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,kind,cycle,dedupe_key)
		 VALUES($1,$2,$3,$4)`,
		serviceID, kind, cycle, key)
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
