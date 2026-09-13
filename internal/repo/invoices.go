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
	DueAt      string
	CreatedAt  string
	// 关联服务（充值等无订单的账单为空，ServiceID 为 0）
	ServiceID     int64
	ServiceName   string
	ServiceHost   string
	ServiceStatus string
}

// CheckoutByNoUser 本地结算页校验：SELECT 账单并 JOIN 网关驱动。
func (iv *Invoices) CheckoutByNoUser(ctx context.Context, no string, userID int64) (id int64, status int16, driver, gateway, baseAmount string, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT i.id,CASE WHEN i.status=0 AND i.due_at IS NOT NULL AND i.due_at <= now() THEN 3 ELSE i.status END,
			g.driver,i.gateway,i.amount::text FROM invoices i JOIN gateways g ON g.code=i.gateway WHERE i.no=$1 AND i.user_id=$2`,
		no, userID).Scan(&id, &status, &driver, &gateway, &baseAmount)
	return
}

// StatusByIDUser 轮询支付状态：SELECT no,status。
func (iv *Invoices) StatusByIDUser(ctx context.Context, id, userID int64) (no string, status int16, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT no,CASE WHEN status=0 AND due_at IS NOT NULL AND due_at <= now() THEN 3 ELSE status END
		 FROM invoices WHERE id=$1 AND user_id=$2`, id, userID).Scan(&no, &status)
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

// LoadByID 按 id 取支付页所需字段：no,amount,status,gateway,credit。
func (iv *Invoices) LoadByID(ctx context.Context, id int64) (no, amount string, status int16, gateway, credit string, err error) {
	err = iv.db.QueryRowContext(ctx,
		`SELECT no,amount,CASE WHEN status=0 AND due_at IS NOT NULL AND due_at <= now() THEN 3 ELSE status END,
			gateway,coalesce(credit,0)::text FROM invoices WHERE id=$1`, id).
		Scan(&no, &amount, &status, &gateway, &credit)
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
	q := `SELECT id,gateway,amount::text,CASE WHEN status=0 AND due_at IS NOT NULL AND due_at <= now() THEN 3 ELSE status END FROM invoices WHERE no=$1 AND user_id=$2`
	if lock {
		q += ` FOR SHARE`
	}
	err = iv.db.QueryRowContext(ctx, q, no, userID).Scan(&invoiceID, &code, &amount, &status)
	return
}

const invoiceListCols = `i.id,i.no,i.amount,coalesce(i.paid_amount,0),coalesce(i.fee_amount,0),i.kind,
	CASE WHEN i.status=0 AND i.due_at IS NOT NULL AND i.due_at <= now() THEN 3 ELSE i.status END,
	to_char(i.due_at,'YYYY-MM-DD HH24:MI'),to_char(i.created_at,'YYYY-MM-DD HH24:MI')`

// invoiceServiceCols 账单关联服务列，与后台订单列表同源：续费/升级订单落在
// orders.service_id，首次开通订单回写在 services.order_id。充值账单没有订单，四列全为空。
const invoiceServiceCols = `coalesce(s.id,0),coalesce(s.name,''),coalesce(s.hostname,''),
	CASE WHEN s.status IS NULL THEN '' WHEN s.status=0 THEN '待开通' WHEN s.status=1 THEN '激活' WHEN s.status=2 THEN '已停机' ELSE '已删除' END`

// invoiceServiceJoin 账单→订单→服务的左连接（充值账单 order_id 为空，服务列自然为 NULL）。
const invoiceServiceJoin = `LEFT JOIN orders o ON o.id=i.order_id
	LEFT JOIN services s ON s.id=coalesce(o.service_id,(SELECT min(sv.id) FROM services sv WHERE sv.order_id=o.id))`

func (iv *Invoices) list(ctx context.Context, q string, args ...any) ([]InvoiceRow, error) {
	rows, err := iv.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvoiceRow
	for rows.Next() {
		var inv InvoiceRow
		if err := rows.Scan(&inv.ID, &inv.No, &inv.Amount, &inv.PaidAmount, &inv.FeeAmount, &inv.Kind, &inv.Status, &inv.DueAt, &inv.CreatedAt,
			&inv.ServiceID, &inv.ServiceName, &inv.ServiceHost, &inv.ServiceStatus); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ListByUser 用户端账单列表（最近 500 条，created_at 已格式化）。
func (iv *Invoices) ListByUser(ctx context.Context, userID int64) ([]InvoiceRow, error) {
	return iv.list(ctx,
		`SELECT `+invoiceListCols+`,`+invoiceServiceCols+`
		 FROM invoices i `+invoiceServiceJoin+`
		 WHERE i.user_id=$1 ORDER BY i.id DESC LIMIT 500`, userID)
}

// ListByService 某服务关联的账单（经订单 service_id 关联；账单跟随服务）。
func (iv *Invoices) ListByService(ctx context.Context, userID, serviceID int64) ([]InvoiceRow, error) {
	return iv.list(ctx,
		`SELECT `+invoiceListCols+`,`+invoiceServiceCols+`
		 FROM invoices i JOIN orders o ON o.id=i.order_id
		 LEFT JOIN services s ON s.id=$2
		 WHERE i.user_id=$1 AND (o.service_id=$2 OR o.id=(SELECT s2.order_id FROM services s2 WHERE s2.id=$2 AND s2.user_id=$1))
		 ORDER BY i.id DESC LIMIT 200`, userID, serviceID)
}
