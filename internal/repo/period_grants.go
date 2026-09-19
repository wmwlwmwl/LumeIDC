package repo

import (
	"context"
	"database/sql"
	"errors"
)

// PeriodGrant 记录某张账单已为服务延长周期，防止重复续费。
type PeriodGrant struct {
	ID        int64
	ServiceID int64
	InvoiceID int64
	Cycle     string
}

// PeriodGrants 服务周期授权操作。
type PeriodGrants struct{ db *sql.DB }

var ErrAlreadyGranted = errors.New("该账单已授权续费")

// Grant 在事务内记录授权；重复 invoice_id 返回 ErrAlreadyGranted。
// 只把 UNIQUE(invoice_id) 冲突当作"已授权"：旧实现把任何错误都当成它，
// 于是连接中断、约束变更等真实故障会被伪装成"该账单已授权续费"，
// 调用方据此中止支付事务并给出误导性结论（钱收了、周期没延长，还查不出原因）。
func (r *PeriodGrants) Grant(ctx context.Context, tx *sql.Tx, serviceID, invoiceID int64, cycle string) error {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO service_period_grants(service_id,invoice_id,cycle) VALUES($1,$2,$3)
		 ON CONFLICT (invoice_id) DO NOTHING`,
		serviceID, invoiceID, cycle)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlreadyGranted
	}
	return nil
}

// HasGranted 检查指定账单是否已授权。
func (r *PeriodGrants) HasGranted(ctx context.Context, invoiceID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM service_period_grants WHERE invoice_id=$1)`,
		invoiceID).Scan(&exists)
	return exists, err
}
