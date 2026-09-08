package repo

import (
	"context"
	"database/sql"
)

// Stats 承载后台仪表盘/列表的只读查询（跨 users/orders/services/invoices 表）。
// SQL 与原先散在 handler 的查询逐字平移。
type Stats struct{ db *sql.DB }

// DashboardCounts 仪表盘三格计数。
type DashboardCounts struct {
	Users    int64 // 用户总数
	Orders   int64 // 已支付订单
	Services int64 // 激活服务
}

// Counts 仪表盘计数。
func (st *Stats) Counts(ctx context.Context) (DashboardCounts, error) {
	var c DashboardCounts
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&c.Users); err != nil {
		return c, err
	}
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE status=1`).Scan(&c.Orders); err != nil {
		return c, err
	}
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM services WHERE status=1`).Scan(&c.Services); err != nil {
		return c, err
	}
	return c, nil
}

// AdminOrderRow 后台订单列表行（金额为字符串保留原文）。
type AdminOrderRow struct {
	ID     int64
	Email  string
	Amount string
	Paid   string
	Fee    string
	Cycle  string
	Status string
	Profit string
}

// AdminOrders 最近 50 条后台订单列表。
func (st *Stats) AdminOrders(ctx context.Context) ([]AdminOrderRow, error) {
	rows, err := st.db.QueryContext(ctx,
		`SELECT o.id,coalesce(u.email,''),o.amount,coalesce(i.paid_amount,0)::text,coalesce(i.fee_amount,0)::text,o.cycle,CASE o.status WHEN 0 THEN '未支付' WHEN 1 THEN '已支付' ELSE '取消' END,coalesce(o.profit,'0')
		 FROM orders o JOIN users u ON u.id=o.user_id LEFT JOIN invoices i ON i.order_id=o.id ORDER BY o.id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminOrderRow
	for rows.Next() {
		var r AdminOrderRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Amount, &r.Paid, &r.Fee, &r.Cycle, &r.Status, &r.Profit); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminOrdersPage 后台订单列表分页+关键词查询，并返回匹配总数。
// q 匹配用户邮箱或订单号；空串表示不过滤。
func (st *Stats) AdminOrdersPage(ctx context.Context, q string, limit, offset int) ([]AdminOrderRow, int64, error) {
	where := `WHERE ($3='' OR u.email ILIKE '%'||$3||'%' OR o.id::text = $3)`
	var total int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT count(*) FROM orders o JOIN users u ON u.id=o.user_id `+where, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := st.db.QueryContext(ctx,
		`SELECT o.id,coalesce(u.email,''),o.amount,coalesce(i.paid_amount,0)::text,coalesce(i.fee_amount,0)::text,o.cycle,CASE o.status WHEN 0 THEN '未支付' WHEN 1 THEN '已支付' ELSE '取消' END,coalesce(o.profit,'0')
		 FROM orders o JOIN users u ON u.id=o.user_id LEFT JOIN invoices i ON i.order_id=o.id `+where+`
		 ORDER BY o.id DESC LIMIT $1 OFFSET $2`, limit, offset, q)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AdminOrderRow
	for rows.Next() {
		var r AdminOrderRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Amount, &r.Paid, &r.Fee, &r.Cycle, &r.Status, &r.Profit); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
