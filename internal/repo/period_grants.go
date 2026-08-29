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
type PeriodGrants struct{ DB *sql.DB }

var ErrAlreadyGranted = errors.New("该账单已授权续费")

// Grant 在事务内记录授权；重复 invoice_id 返回 ErrAlreadyGranted。
func (r *PeriodGrants) Grant(ctx context.Context, tx *sql.Tx, serviceID, invoiceID int64, cycle string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO service_period_grants(service_id,invoice_id,cycle) VALUES($1,$2,$3)`,
		serviceID, invoiceID, cycle)
	if err != nil {
		// UNIQUE(invoice_id) 冲突 = 已授权
		return ErrAlreadyGranted
	}
	return nil
}

// HasGranted 检查指定账单是否已授权。
func (r *PeriodGrants) HasGranted(ctx context.Context, invoiceID int64) (bool, error) {
	var exists bool
	err := r.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM service_period_grants WHERE invoice_id=$1)`,
		invoiceID).Scan(&exists)
	return exists, err
}
