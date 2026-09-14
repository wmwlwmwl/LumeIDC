package repo

import (
 "context"
 "database/sql"
 "database/sql/driver"
 "errors"
 "fmt"
 "time"
)

var ErrFulfillmentBusy = errors.New("该服务仍有执行者或资金操作，请稍后重新核对")

func FulfillmentLockKey(serviceID int64) string { return fmt.Sprintf("fulfillment-service:%d", serviceID) }

// TryExecutionLock 不持连接排队等锁；同一服务的执行、恢复和退款共用锁键。
func (r *FulfillmentJobs) TryExecutionLock(ctx context.Context, serviceID int64) (*sql.Conn, func(), error) {
 conn, err := r.db.Conn(ctx)
 if err != nil { return nil, nil, err }
 key := FulfillmentLockKey(serviceID)
 var locked bool
 if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil || !locked {
  conn.Close()
  if err != nil { return nil,nil,err }; return nil,nil,ErrFulfillmentBusy
 }
 return conn, func() {
  cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel()
  if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
   _ = conn.Raw(func(any) error { return driver.ErrBadConn })
  }
  _ = conn.Close()
 }, nil
}

func (r *FulfillmentJobs) ValidateClaim(ctx context.Context, job *FulfillmentJob) error {
 var valid bool
 err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id=$1 AND service_id=$2
 AND claim_version=$3 AND status='running' AND NOT recovery_required AND lease_until>clock_timestamp())`, job.ID,job.ServiceID,job.ClaimVersion).Scan(&valid)
 if err != nil { return err }; if !valid { return ErrFulfillmentLeaseLost }; return nil
}
