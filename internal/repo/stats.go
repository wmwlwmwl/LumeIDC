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

// TrendPoint 仪表盘趋势点（按天，PostgreSQL generate_series 补齐空白天）。
type TrendPoint struct {
	Date    string  // MM-DD
	Users   int64   // 当日注册用户
	Orders  int64   // 当日已支付订单
	Revenue float64 // 当日已支付金额合计
}

// Trends 最近 days 天的注册/已支付订单/收入趋势（含无数据的天，补 0）。
// 两个聚合 CTE 必须自带同样的日期下界：否则每次刷新都对 users/orders 全表聚合，
// 数据一多仪表盘就随历史量线性变慢（generate_series 只补空白，不缩小聚合范围）。
func (st *Stats) Trends(ctx context.Context, days int) ([]TrendPoint, error) {
	rows, err := st.db.QueryContext(ctx, `
		WITH d AS (
			SELECT generate_series((current_date - ($1::int - 1))::date, current_date, interval '1 day')::date AS day
		),
		u AS (SELECT created_at::date AS day, count(*) AS cnt FROM users
			WHERE created_at >= (current_date - ($1::int - 1))::date GROUP BY 1),
		o AS (SELECT created_at::date AS day, count(*) AS cnt, coalesce(sum(amount), 0) AS sum FROM orders
			WHERE status=1 AND created_at >= (current_date - ($1::int - 1))::date GROUP BY 1)
		SELECT to_char(d.day, 'MM-DD'), coalesce(u.cnt, 0), coalesce(o.cnt, 0), coalesce(o.sum, 0)::float8
		FROM d LEFT JOIN u ON u.day = d.day LEFT JOIN o ON o.day = d.day
		ORDER BY d.day`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrendPoint, 0, days)
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Date, &p.Users, &p.Orders, &p.Revenue); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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
	// 关联服务（未开通的订单为空）
	ServiceName   string
	ServiceHost   string
	ServiceStatus string
}

// adminOrderSelect 后台订单列表共用查询列。
// 关联服务有两条路径：续费/升级订单直接落在 orders.service_id，首次开通订单则是
// 开通后回写到 services.order_id，这里用 coalesce 取其一。
const adminOrderSelect = `SELECT o.id,coalesce(u.email,''),o.amount,coalesce(i.paid_amount,0)::text,coalesce(i.fee_amount,0)::text,
 CASE o.cycle WHEN 'monthly' THEN '月付' WHEN 'quarterly' THEN '季付' WHEN 'yearly' THEN '年付' ELSE o.cycle END,
 CASE o.status WHEN 0 THEN '未支付' WHEN 1 THEN '已支付' ELSE '取消' END,coalesce(o.profit,'0'),
 coalesce(s.name,''),coalesce(s.hostname,''),
 CASE WHEN s.status IS NULL THEN '' WHEN s.status=0 THEN '待开通' WHEN s.status=1 THEN '激活' WHEN s.status=2 THEN '已停机' ELSE '已删除' END
 FROM orders o JOIN users u ON u.id=o.user_id LEFT JOIN invoices i ON i.order_id=o.id
 LEFT JOIN services s ON s.id=coalesce(o.service_id,(SELECT min(sv.id) FROM services sv WHERE sv.order_id=o.id))`

// scanAdminOrders 逐行扫描后台订单列表。
func scanAdminOrders(rows *sql.Rows) ([]AdminOrderRow, error) {
	var out []AdminOrderRow
	for rows.Next() {
		var r AdminOrderRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Amount, &r.Paid, &r.Fee, &r.Cycle, &r.Status, &r.Profit,
			&r.ServiceName, &r.ServiceHost, &r.ServiceStatus); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OrderByParam 把 ?sort=&order= 请求参数映射到白名单 SQL 排序片段（含前导空格，
// 供直接拼接 WHERE 之后）。未传或不在白名单时返回 fallback（各列表默认排序）；
// 表达式全部来自调用方硬编码白名单，order 仅接受 asc/desc，无注入面。
func OrderByParam(sort, order string, cols map[string]string, fallback string) string {
	expr, ok := cols[sort]
	if !ok {
		return fallback
	}
	dir := " DESC"
	if order == "asc" {
		dir = " ASC"
	}
	return " ORDER BY " + expr + dir
}

// adminOrderSort 后台订单列表可排序字段白名单（prop → SQL 表达式）。
var adminOrderSort = map[string]string{
	"id":     "o.id",
	"email":  "u.email",
	"amount": "o.amount",
	"paid":   "coalesce(i.paid_amount,0)",
	"profit": "coalesce(o.profit,'0')::numeric",
	"status": "o.status",
}

// AdminOrdersPage 后台订单列表分页+关键词查询，并返回匹配总数。
// q 匹配用户邮箱或订单号；空串表示不过滤。sort/order 经白名单映射排序字段。
func (st *Stats) AdminOrdersPage(ctx context.Context, q, sort, order string, limit, offset int) ([]AdminOrderRow, int64, error) {
	where := `WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR o.id::text = $1)`
	var total int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT count(*) FROM orders o JOIN users u ON u.id=o.user_id `+where, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := st.db.QueryContext(ctx,
		adminOrderSelect+` `+where+OrderByParam(sort, order, adminOrderSort, ` ORDER BY o.id DESC`)+`
			 LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanAdminOrders(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
