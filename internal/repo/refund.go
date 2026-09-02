package repo

import (
	"context"
	"database/sql"
	"time"
)

// Refunds 退款记录。
type Refunds struct{ db *sql.DB }

type RefundRow struct {
	ID        int64
	UserID    int64
	OrderID   int64
	InvoiceID int64
	Amount    string
	Method    string
	Reason    string
	AdminID   int64
	Status    string
	CreatedAt time.Time
}

func (r *Refunds) Create(ctx context.Context, row RefundRow) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO refunds(user_id,order_id,invoice_id,amount,method,reason,admin_id,status)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		row.UserID, row.OrderID, row.InvoiceID, row.Amount, row.Method, row.Reason, row.AdminID, row.Status)
	return err
}

func (r *Refunds) List(ctx context.Context, limit int) ([]RefundRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,user_id,order_id,invoice_id,amount,method,reason,admin_id,status,created_at
		 FROM refunds ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundRow
	for rows.Next() {
		var rw RefundRow
		if err := rows.Scan(&rw.ID, &rw.UserID, &rw.OrderID, &rw.InvoiceID, &rw.Amount,
			&rw.Method, &rw.Reason, &rw.AdminID, &rw.Status, &rw.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rw)
	}
	return out, rows.Err()
}

// RefundedSum 某订单已退金额（numeric 文本）。
func (r *Refunds) RefundedSum(ctx context.Context, orderID int64) (string, error) {
	var s sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount::numeric),0)::text FROM refunds WHERE order_id=$1`, orderID).Scan(&s)
	if s.Valid {
		return s.String, err
	}
	return "0", err
}
