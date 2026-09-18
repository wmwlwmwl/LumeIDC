package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type FulfillmentJob struct {
	ID           int64
	ServiceID    int64
	OrderID      sql.NullInt64
	Kind         string
	Cycle        string
	Attempts     int
	ClaimVersion int64
	// DedupeKey 任务业务标识（自动任务为 kind:订单号，人工重试带时间戳）。
	// 全局唯一，管理员告警用它做去重键：同一任务只告警一次，人工重试生成的新任务会重新告警。
	DedupeKey string
}

type FulfillmentJobs struct{ db *sql.DB }

var ErrFulfillmentLeaseLost = errors.New("履约任务领取权已失效，结果未写入，请核对上游结果")
var ErrFulfillmentRecoveryRequired = errors.New("履约任务中断，上游结果未知；请核对上游账单和实例并完成对账，禁止直接重试")

func (r *FulfillmentJobs) EnqueueTx(ctx context.Context, tx *sql.Tx, serviceID, orderID int64, kind, cycle string) error {
	key := fmt.Sprintf("%s:%d", kind, orderID)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,dedupe_key)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT (dedupe_key) DO NOTHING`,
		serviceID, orderID, kind, cycle, key)
	return err
}

// recoverExpired 只隔离，不重放上游操作；空租约的历史 running 同样视为未知。
// 服务行锁与领取、人工重试共用，隔离状态与后台提示在同一事务提交。
// ponytail: 每次最多处理 100 个服务；积压由后续轮询恢复，规模扩大时改为独立分批恢复任务。
func (r *FulfillmentJobs) recoverExpired(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.id FROM services s
		WHERE EXISTS (SELECT 1 FROM fulfillment_jobs j WHERE j.service_id=s.id
		 AND j.status='running' AND (j.lease_until IS NULL OR j.lease_until<=now()))
		ORDER BY s.id FOR UPDATE OF s SKIP LOCKED LIMIT 100`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		var kind string
		err := tx.QueryRowContext(ctx, `SELECT kind FROM fulfillment_jobs
			WHERE service_id=$1 AND status='running' AND (lease_until IS NULL OR lease_until<=now())
			ORDER BY id LIMIT 1 FOR UPDATE`, id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE services SET provision_error=$2,
			transition_state=CASE WHEN coalesce(transition_state,'')='' AND $3='renew' THEN 'renew_pending'
			 WHEN coalesce(transition_state,'')='' AND $3='upgrade' THEN 'upgrading' ELSE transition_state END
			WHERE id=$1`, id, ErrFulfillmentRecoveryRequired.Error(), kind); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fulfillment_jobs SET status='manual_review',recovery_required=true,
			lease_until=NULL,last_error=$2,updated_at=now()
			WHERE service_id=$1 AND status='running' AND (lease_until IS NULL OR lease_until<=now())`,
			id, ErrFulfillmentRecoveryRequired.Error()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *FulfillmentJobs) Claim(ctx context.Context, lease time.Duration) (*FulfillmentJob, error) {
	if lease <= 0 {
		return nil, errors.New("履约租约时长必须大于零")
	}
	if err := r.recoverExpired(ctx); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var serviceID int64
	err = tx.QueryRowContext(ctx, `SELECT s.id FROM services s
		WHERE EXISTS (SELECT 1 FROM fulfillment_jobs j WHERE j.service_id=s.id
		 AND j.status IN ('queued','retry') AND j.next_attempt_at<=now()
		 AND (j.lease_until IS NULL OR j.lease_until<=now()))
		AND NOT EXISTS (SELECT 1 FROM fulfillment_jobs j WHERE j.service_id=s.id
		 AND (j.status='running' OR j.recovery_required))
		ORDER BY (SELECT min(j.id) FROM fulfillment_jobs j WHERE j.service_id=s.id AND j.status IN ('queued','retry'))
		FOR UPDATE OF s SKIP LOCKED LIMIT 1`).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// 锁定服务后用新快照再次判断，避免等待锁期间旧任务刚被领取或隔离。
	var j FulfillmentJob
	err = tx.QueryRowContext(ctx, `SELECT id,service_id,order_id,kind,cycle,attempts,dedupe_key
		FROM fulfillment_jobs WHERE service_id=$1 AND status IN ('queued','retry') AND next_attempt_at<=now()
		AND (lease_until IS NULL OR lease_until<=now())
		AND NOT EXISTS (SELECT 1 FROM fulfillment_jobs x WHERE x.service_id=$1 AND (x.status='running' OR x.recovery_required))
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`, serviceID).
		Scan(&j.ID, &j.ServiceID, &j.OrderID, &j.Kind, &j.Cycle, &j.Attempts, &j.DedupeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx,
		`UPDATE fulfillment_jobs SET status='running',attempts=attempts+1,claim_version=claim_version+1,
		 lease_until=clock_timestamp()+($2 * interval '1 second'),updated_at=now() WHERE id=$1 RETURNING claim_version`,
		j.ID, lease.Seconds()).Scan(&j.ClaimVersion)
	if err != nil {
		return nil, err
	}
	j.Attempts++
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &j, nil
}

func fulfillmentWriteResult(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrFulfillmentLeaseLost
	}
	return nil
}

func (r *FulfillmentJobs) Complete(ctx context.Context, job *FulfillmentJob) error {
	return fulfillmentWriteResult(r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='succeeded',lease_until=NULL,last_error='',updated_at=now()
		 WHERE id=$1 AND claim_version=$2 AND status='running' AND lease_until>clock_timestamp() AND NOT recovery_required`, job.ID, job.ClaimVersion))
}

// HasRunningJob 不把过期任务当作已完成：恢复由 Claim 的隔离事务处理。
func (r *FulfillmentJobs) HasRunningJob(ctx context.Context, serviceID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=$1 AND status='running')`,
		serviceID).Scan(&exists)
	return exists, err
}

func checkFulfillmentRetry(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, serviceID int64) error {
	var running, recovery bool
	if err := q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=$1 AND status='running'),
		EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=$1 AND recovery_required)`, serviceID).Scan(&running, &recovery); err != nil {
		return err
	}
	if recovery {
		return ErrFulfillmentRecoveryRequired
	}
	if running {
		return errors.New("该服务已有正在执行或等待复核的任务，请等待处理后再试")
	}
	return nil
}

// CheckRetry 在后台取消操作前拒绝正在执行或未知结果的任务；人工入队另在事务内检查。
func (r *FulfillmentJobs) CheckRetry(ctx context.Context, serviceID int64) error {
	return checkFulfillmentRetry(ctx, r.db, serviceID)
}

const fulfillmentRetryBusiness = `a.id<b.id AND a.service_id=b.service_id AND a.kind=b.kind AND a.cycle=b.cycle
 AND coalesce(a.order_id,CASE WHEN a.kind='provision' THEN coalesce(s.order_id,0) END)=coalesce(b.order_id,CASE WHEN b.kind='provision' THEN coalesce(s.order_id,0) END)
 AND b.dedupe_key ~ ('^retry:' || b.kind || ':' || b.service_id::text || ':[0-9]+$')`

const fulfillmentSuperseded = `EXISTS (SELECT 1 FROM fulfillment_jobs b JOIN services s ON s.id=b.service_id
 WHERE a.superseded_by=b.id AND a.status='dead' AND NOT a.recovery_required AND a.lease_until IS NULL
 AND ` + fulfillmentRetryBusiness + `)`

func PendingFulfillmentJobsTx(ctx context.Context, tx *sql.Tx, serviceID int64) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fulfillment_jobs a WHERE service_id=$1
 AND (recovery_required OR (status<>'succeeded' AND NOT `+fulfillmentSuperseded+`))`, serviceID).Scan(&count)
	return count, err
}

// FulfillmentRetryHistoryTx 仅识别人工重试之前、同业务且无未知结果的历史。
// 开通允许旧人工任务缺少订单号；续费/升级必须明确属于同一订单，不能仅凭服务合并。
func FulfillmentRetryHistoryTx(ctx context.Context, tx *sql.Tx, jobID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.id FROM fulfillment_jobs a
 JOIN fulfillment_jobs b ON b.id=$1 JOIN services s ON s.id=b.service_id
 WHERE `+fulfillmentRetryBusiness+`
 AND a.superseded_by IS NULL AND NOT a.recovery_required AND a.lease_until IS NULL
 AND a.created_at<=a.updated_at AND a.updated_at<=b.created_at
 AND a.status IN ('manual_review','dead') ORDER BY a.id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SupersedeFulfillmentHistoryTx 保留失败证据，不把被接替的任务冒充成功；调用者须持服务行锁。
func SupersedeFulfillmentHistoryTx(ctx context.Context, tx *sql.Tx, jobID int64, ids []int64) error {
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `UPDATE fulfillment_jobs a SET status='dead',superseded_by=b.id
 FROM fulfillment_jobs b JOIN services s ON s.id=b.service_id
 WHERE a.id=$1 AND b.id=$2 AND `+fulfillmentRetryBusiness+`
 AND a.superseded_by IS NULL AND NOT a.recovery_required AND a.lease_until IS NULL
 AND a.created_at<=a.updated_at AND a.updated_at<=b.created_at
 AND a.status IN ('manual_review','dead')`, id, jobID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("历史履约任务已变化，禁止接替，请重新核对")
		}
	}
	return nil
}

// EnqueueRetry 将人工确认、历史收敛及入队一起提交，避免失败时覆盖未知上游结果的提示。
func (r *FulfillmentJobs) EnqueueRetry(ctx context.Context, serviceID, orderID int64, kind, cycle string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var originalOrder int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(order_id,0) FROM services WHERE id=$1 FOR UPDATE`, serviceID).Scan(&originalOrder); err != nil {
		return err
	}
	if err := checkFulfillmentRetry(ctx, tx, serviceID); err != nil {
		return err
	}
	if kind == "provision" {
		if orderID != 0 && orderID != originalOrder {
			return errors.New("开通重试订单与服务不一致")
		}
		orderID = originalOrder
	} else if (kind != "renew" && kind != "upgrade") || orderID <= 0 {
		return errors.New("重试任务类型或订单号无效")
	}
	if kind == "renew" {
		if orderID <= 0 {
			return errors.New("续费任务缺少订单号")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE services SET provision_data=jsonb_set(coalesce(provision_data,'{}'::jsonb),ARRAY[$2]::text[],'"1"'::jsonb,true) WHERE id=$1`, serviceID, fmt.Sprintf("renew_price_ok_%d", orderID)); err != nil {
			return err
		}
	}
	if kind == "renew" || kind == "upgrade" {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET provision_error='' WHERE id=$1`, serviceID); err != nil {
			return err
		}
	}
	key := fmt.Sprintf("retry:%s:%d:%d", kind, serviceID, time.Now().UnixNano())
	var jobID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,dedupe_key)
		 VALUES($1,nullif($2,0),$3,$4,$5) RETURNING id`, serviceID, orderID, kind, cycle, key).Scan(&jobID); err != nil {
		return err
	}
	history, err := FulfillmentRetryHistoryTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	pending, err := PendingFulfillmentJobsTx(ctx, tx, serviceID)
	if err != nil {
		return err
	}
	if pending != len(history)+1 {
		return errors.New("该服务还有其他未决履约任务，请先处理，禁止重复入队")
	}
	if err := SupersedeFulfillmentHistoryTx(ctx, tx, jobID, history); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *FulfillmentJobs) Fail(ctx context.Context, job *FulfillmentJob, cause error) error {
	status := "retry"
	if job.Attempts >= 8 {
		status = "dead"
	}
	shift := min(max(job.Attempts, 0), 10)
	delay := time.Duration(1<<shift) * time.Minute
	return fulfillmentWriteResult(r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status=$3,lease_until=NULL,last_error=$4,
		 next_attempt_at=now()+($5 * interval '1 second'),updated_at=now()
		 WHERE id=$1 AND claim_version=$2 AND status='running' AND lease_until>clock_timestamp() AND NOT recovery_required`,
		job.ID, job.ClaimVersion, status, fulfillmentError(cause, "未知错误"), delay.Seconds()))
}

// RetryLater 不消耗重试次数，但领取版本始终单调增加。
// ponytail: 上游若长期欠费，任务按 interval 一直留队；真正无解的失败应转人工。
func (r *FulfillmentJobs) RetryLater(ctx context.Context, job *FulfillmentJob, cause error, interval time.Duration) error {
	return fulfillmentWriteResult(r.db.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='retry',attempts=0,lease_until=NULL,last_error=$3,
		 next_attempt_at=now()+($4 * interval '1 second'),updated_at=now()
		 WHERE id=$1 AND claim_version=$2 AND status='running' AND lease_until>clock_timestamp() AND NOT recovery_required`,
		job.ID, job.ClaimVersion, fulfillmentError(cause, "等待外部条件"), interval.Seconds()))
}

// MarkManualReview 的 recoveryRequired 区分未知结果与已知的价格/余额暂停。
func (r *FulfillmentJobs) MarkManualReview(ctx context.Context, job *FulfillmentJob, cause error, recoveryRequired bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE id=$1 FOR UPDATE`, job.ServiceID).Scan(&id); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM fulfillment_jobs WHERE id=$1 AND service_id=$3
		AND claim_version=$2 AND status='running' AND lease_until>clock_timestamp() AND NOT recovery_required FOR UPDATE`,
		job.ID, job.ClaimVersion, job.ServiceID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFulfillmentLeaseLost
		}
		return err
	}
	if recoveryRequired {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET provision_error=$2,
			transition_state=CASE WHEN coalesce(transition_state,'')='' AND $3='renew' THEN 'renew_pending'
			 WHEN coalesce(transition_state,'')='' AND $3='upgrade' THEN 'upgrading' ELSE transition_state END WHERE id=$1`,
			job.ServiceID, ErrFulfillmentRecoveryRequired.Error(), job.Kind); err != nil {
			return err
		}
	}
	if err := fulfillmentWriteResult(tx.ExecContext(ctx,
		`UPDATE fulfillment_jobs SET status='manual_review',lease_until=NULL,last_error=$3,recovery_required=$4,updated_at=now()
		 WHERE id=$1 AND claim_version=$2 AND status='running' AND lease_until>clock_timestamp() AND NOT recovery_required`,
		job.ID, job.ClaimVersion, fulfillmentError(cause, "需要人工复核"), recoveryRequired)); err != nil {
		return err
	}
	return tx.Commit()
}

func fulfillmentError(cause error, fallback string) string {
	if cause == nil {
		return fallback
	}
	msg := []rune(cause.Error())
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return string(msg)
}
