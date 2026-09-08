package repo

import (
	"context"
	"database/sql"
)

// Invoices 封装 invoices 表的读取/校验查询。写操作与支付核销在 service 层。
// SQL 与原先散布在 handler 的查询逐字平移，语义不变。
type Invoices struct{ db *sql.DB }

// InvoiceRow 用户端账单列表行（金额保留数据库原文）。
type InvoiceRow struct {
	ID         int64
	No         string
	Amount     string
	PaidAmount string
	FeeAmount  string
	Kind       string
	Status     int16
	CreatedAt  string
}

// F2FByNoUser 支付宝当面付二维码页校验：SELECT 账单并 JOIN 网关驱动。
func (iv *Invoices) F2FByNoUser(ctx context.Context, no string, userID int64) (id int64, status int16, driver, gateway, baseAmount string, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT i.id,i.status,g.driver,i.gateway,i.amount::text FROM invoices i JOIN gateways g ON g.code=i.gateway WHERE i.no=$1 AND i.user_id=$2`,
		no, userID).Scan(&id, &status, &driver, &gateway, &baseAmount)
	return
}

// StatusByIDUser 轮询支付状态：SELECT no,status。
func (iv *Invoices) StatusByIDUser(ctx context.Context, id, userID int64) (no string, status int16, err error) {
	err = iv.db.QueryRowContext(ctx, `SELECT no,status FROM invoices WHERE id=$1 AND user_id=$2`, id, userID).Scan(&no, &status)
	return
}

// NoByID 按 id 取账单号（0 元自动核销用）。
func (iv *Invoices) NoByID(ctx context.Context, id int64) (string, error) {
	var no string
	err := iv.db.QueryRowContext(ctx, `SELECT no FROM invoices WHERE id=$1`, id).Scan(&no)
	return no, err
}

// KindByID 按 id 取账单类型（recharge/order）。
func (iv *Invoices) KindByID(ctx context.Context, id int64) (string, error) {
	var kind string
	err := iv.db.QueryRowContext(ctx, `SELECT kind FROM invoices WHERE id=$1`, id).Scan(&kind)
	return kind, err
}

// LoadByID 按 id 取支付页所需字段：no,amount,status,gateway。
func (iv *Invoices) LoadByID(ctx context.Context, id int64) (no, amount string, status int16, gateway string, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT no,amount,status,gateway FROM invoices WHERE id=$1`, id).
		Scan(&no, &amount, &status, &gateway)
	return
}

// OwnedByNoUser 校验账单归属：按 (no,user_id) 恰有一条记录。
func (iv *Invoices) OwnedByNoUser(ctx context.Context, no string, userID int64) (bool, error) {
	var n int64
	if err := iv.db.QueryRowContext(ctx,
		`SELECT count(*) FROM invoices WHERE no=$1 AND user_id=$2`, no, userID).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

// RecordByNo 按账单号取核销审计字段：status,gateway,trade_no,paid_amount（回调去重校验用）。
func (iv *Invoices) RecordByNo(ctx context.Context, no string) (status int16, gateway, tradeNo, paidAmount string, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT status,gateway,trade_no,paid_amount::text FROM invoices WHERE no=$1`, no).
		Scan(&status, &gateway, &tradeNo, &paidAmount)
	return
}

// MockByNoUser 模拟网关页：按 (no,user_id) 取 id,gateway,amount,status；lock=true 时加 FOR SHARE。
func (iv *Invoices) MockByNoUser(ctx context.Context, no string, userID int64, lock bool) (invoiceID int64, code, amount string, status int16, err error) {
	q := `SELECT id,gateway,amount::text,status FROM invoices WHERE no=$1 AND user_id=$2`
	if lock {
		q += ` FOR SHARE`
	}
	err = iv.db.QueryRowContext(ctx, q, no, userID).Scan(&invoiceID, &code, &amount, &status)
	return
}

// ListByUser 用户端账单列表（最近 100 条，created_at 已格式化）。
func (iv *Invoices) ListByUser(ctx context.Context, userID int64) ([]InvoiceRow, error) {
	rows, err := iv.db.QueryContext(ctx,
		`SELECT id,no,amount,coalesce(paid_amount,0),coalesce(fee_amount,0),kind,status,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM invoices WHERE user_id=$1 ORDER BY id DESC LIMIT 500`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvoiceRow
	for rows.Next() {
		var inv InvoiceRow
		if err := rows.Scan(&inv.ID, &inv.No, &inv.Amount, &inv.PaidAmount, &inv.FeeAmount, &inv.Kind, &inv.Status, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}
