package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type ServiceRow struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Status       int16     `json:"status"`
	StatusText   string    `json:"-"`
	ExpiresAt    time.Time `json:"expires_at"`
	ProductID    int64     `json:"product_id"`
	ShowQ        bool      `json:"-"`
	ShowY        bool      `json:"-"`
	ExpiringSoon bool      `json:"-"` // 14 天内到期（列表提醒用）
	Hostname     string    `json:"hostname"`
	IP           string    `json:"ip"` // 上游实时（best-effort，列表展示）
	OS           string    `json:"os"` // 系统名称/版本（best-effort）
	Monthly      string    `json:"-"`  // 月售价（含配置+利润）
	ConfigDesc   string    `json:"-"`  // 配置摘要（如 "CPU 2核 · 内存 4G"）
	DaysLeft     int       `json:"-"`  // 距到期天数
	// 内存计价数据（一次查询带回，避免逐行 N 次远程查询）
	ConfigSnap        []byte // coalesce(sv.config_snapshot, o.config_snapshot)
	ConfigOpts        []byte // products.configoption
	MonthlyBase       string // 默认价格组月价
	QuarterlyBase     string
	YearlyBase        string
	ProfitType        int16
	ProfitValue       float64
	ServerProfitType  int16
	ServerProfitValue float64
}

type ServicesRepo struct{ db *sql.DB }

func (s *ServicesRepo) ListByUser(ctx context.Context, userID int64) ([]ServiceRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sv.id, sv.name, sv.status, coalesce(sv.expires_at,sv.created_at), sv.product_id, coalesce(sv.hostname,''),
		        coalesce(sv.config_snapshot, o.config_snapshot),
		        coalesce(p.configoption::text,'[]'),
		        coalesce(pp.monthly::text,'0'), coalesce(pp.quarterly::text,'0'), coalesce(pp.yearly::text,'0'),
		        p.profit_type, p.profit_value, coalesce(s.profit_type,0), coalesce(s.profit_value,0)
		 FROM services sv JOIN products p ON p.id=sv.product_id
		 LEFT JOIN servers s ON s.id=p.server_id
		 LEFT JOIN orders o ON o.id=sv.order_id
		 LEFT JOIN product_prices pp ON pp.product_id=p.id AND pp.priceset_id=(SELECT min(id) FROM pricesets)
		 WHERE sv.user_id=$1 AND sv.status<3 ORDER BY sv.id DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceRow
	for rows.Next() {
		var sr ServiceRow
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Status, &sr.ExpiresAt, &sr.ProductID, &sr.Hostname,
			&sr.ConfigSnap, &sr.ConfigOpts, &sr.MonthlyBase, &sr.QuarterlyBase, &sr.YearlyBase,
			&sr.ProfitType, &sr.ProfitValue, &sr.ServerProfitType, &sr.ServerProfitValue); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// ConfigSnapshot 下单时的配置项快照。
type ConfigSnapshot struct {
	Name  string  `json:"name"`
	Value string  `json:"value"`
	Price float64 `json:"price"`
}

// ServiceDetail 服务详情。
type ServiceDetail struct {
	ServiceRow
	ProductID    int64            `json:"product_id"`
	Hostname     string           `json:"hostname"`
	CreatedAt    time.Time        `json:"created_at"`
	Cycle        string           `json:"cycle"`
	Amount       string           `json:"amount"`
	Configs      []ConfigSnapshot `json:"configs"`
	UpstreamHost int64            `json:"upstream_host_id"`
	Remark       string           `json:"remark"`
}

// GetDetail 读取服务详情（归属校验在 SQL 内）。
func (s *ServicesRepo) GetDetail(ctx context.Context, serviceID, userID int64) (*ServiceDetail, error) {
	var d ServiceDetail
	var orderID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT sv.id, sv.name, sv.status, coalesce(sv.expires_at,sv.created_at), sv.hostname,
		        sv.created_at, sv.upstream_host_id, o.id, sv.product_id, sv.remark
		 FROM services sv LEFT JOIN orders o ON o.id = sv.order_id
		 WHERE sv.id=$1 AND sv.user_id=$2`, serviceID, userID).
		Scan(&d.ID, &d.Name, &d.Status, &d.ExpiresAt, &d.Hostname,
			&d.CreatedAt, &d.UpstreamHost, &orderID, &d.ProductID, &d.Remark)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errSvcNotFound
	}
	if err != nil {
		return nil, err
	}
	statusText := map[int16]string{0: "待开通", 1: "激活", 2: "已停机", 3: "已删除"}
	d.StatusText = statusText[d.Status]
	// 周期/金额与配置快照均取本服务自己所属订单（config_snapshot），
	// 不再取“该用户该产品最早一条”快照——否则多单用户会看到别的订单的配置。
	// 升降级后：services.cycle/config_snapshot 优先于原订单（升级换了产品/配置/周期）。
	if orderID.Valid {
		var oCycle, amount, svCycle, svSnap string
		var oSnap []byte
		if err := s.db.QueryRowContext(ctx,
			`SELECT o.cycle, o.amount, coalesce(o.config_snapshot::text,''),
			        coalesce(sv.cycle,''), coalesce(sv.config_snapshot::text,'')
			 FROM orders o JOIN services sv ON sv.id=o.service_id WHERE o.id=$1`,
			orderID.Int64).Scan(&oCycle, &amount, &oSnap, &svCycle, &svSnap); err == nil {
			cycle := oCycle
			if svCycle != "" {
				cycle = svCycle
			}
			snap := oSnap
			if len(svSnap) > 0 {
				snap = []byte(svSnap)
			}
			d.Cycle = map[string]string{"monthly": "月付", "quarterly": "季付", "yearly": "年付"}[cycle]
			d.Amount = amount
			if len(snap) > 0 {
				var qs struct {
					Quote struct {
						Config []struct {
							Name  string  `json:"name"`
							Value string  `json:"value"`
							Price float64 `json:"price"`
						} `json:"config"`
					} `json:"quote"`
				}
				if json.Unmarshal(snap, &qs) == nil {
					for _, c := range qs.Quote.Config {
						d.Configs = append(d.Configs, ConfigSnapshot{Name: c.Name, Value: c.Value, Price: c.Price})
					}
				}
			}
		}
	}
	return &d, nil
}

// Rename 用户修改服务名称与备注（归属由调用方校验）。
func (s *ServicesRepo) Rename(ctx context.Context, serviceID int64, name, remark string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE services SET name=$2, remark=$3 WHERE id=$1`, serviceID, name, remark)
	return err
}

// ConfigSelection 读取服务当前生效的配置选择（services.config_snapshot 优先，回退原订单快照）。
// 供升降级差价/详情展示；无快照返回空 map。
func (s *ServicesRepo) ConfigSelection(ctx context.Context, serviceID int64) map[string]string {
	var snap []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT coalesce(sv.config_snapshot, o.config_snapshot)
		 FROM services sv LEFT JOIN orders o ON o.id=sv.order_id WHERE sv.id=$1`,
		serviceID).Scan(&snap); err != nil {
		return nil
	}
	var saved struct {
		Selection map[string]string `json:"selection"`
	}
	if json.Unmarshal(snap, &saved) == nil {
		return saved.Selection
	}
	return nil
}

var errSvcNotFound = svcErr("服务不存在")

// ServiceLog 服务操作日志条目（本地记录，非上游数据）。
type ServiceLog struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AppendLog 记录一次用户操作；日志失败只记 warning，不影响主流程。
func (s *ServicesRepo) AppendLog(ctx context.Context, serviceID, userID int64, action, detail string) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO service_logs (service_id, user_id, action, detail) VALUES ($1,$2,$3,$4)`,
		serviceID, userID, action, detail); err != nil {
		log.Printf("[svclog] service %d 写操作日志失败: %v", serviceID, err)
	}
}

// Logs 最近 50 条操作日志（归属校验在 SQL 内）。
func (s *ServicesRepo) Logs(ctx context.Context, serviceID, userID int64) ([]ServiceLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, action, detail, created_at FROM service_logs
		 WHERE service_id=$1 AND user_id=$2 ORDER BY id DESC LIMIT 50`, serviceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceLog
	for rows.Next() {
		var l ServiceLog
		if err := rows.Scan(&l.ID, &l.Action, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type svcErr string

func (e svcErr) Error() string { return string(e) }

// UserStats 账户概览统计：服务数/激活数/累计支付/未支付账单数（SQL 与旧 handler 内查询逐字平移）。
func (s *ServicesRepo) UserStats(ctx context.Context, userID int64) (serviceCount, activeCount, unpaidCount int64, paidTotal string) {
	_ = s.db.QueryRowContext(ctx,
		`SELECT count(*),count(*) FILTER (WHERE status=1) FROM services WHERE user_id=$1 AND status<3`, userID).
		Scan(&serviceCount, &activeCount)
	_ = s.db.QueryRowContext(ctx,
		`SELECT coalesce(sum(coalesce(paid_amount,amount)),0) FROM invoices WHERE user_id=$1 AND status=1`, userID).
		Scan(&paidTotal)
	_ = s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM invoices WHERE user_id=$1 AND status=0`, userID).
		Scan(&unpaidCount)
	return
}

// Owns 是否拥有该服务（任意状态）。
func (s *ServicesRepo) Owns(ctx context.Context, serviceID, userID int64) (bool, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM services WHERE id=$1 AND user_id=$2`, serviceID, userID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// OwnsActive 是否拥有且服务处于激活/停机（status IN (1,2)）。
func (s *ServicesRepo) OwnsActive(ctx context.Context, serviceID, userID int64) (bool, error) {
	var owned bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id=$1 AND user_id=$2 AND status IN (1,2))`, serviceID, userID).Scan(&owned)
	return owned, err
}

// AdminServiceRow 后台服务列表行（SQL 与旧 handler 查询逐字平移；含内存计价所需数据）。
type AdminServiceRow struct {
	ID        int64
	User      string // email
	Name      string
	Status    string
	Expires   string
	Upstream  string // upstream host id
	ProvErr   string
	Profit    string
	ProductID int64
	Hostname  string
	ExpiresAt time.Time
	// 内存计价数据（一次查询带回，避免逐行 N 次远程查询）
	ConfigSnap        []byte // coalesce(sv.config_snapshot, o.config_snapshot)
	ConfigOpts        []byte // products.configoption
	MonthlyBase       string // 默认价格组月价
	ProfitType        int16  // 产品利润
	ProfitValue       float64
	ServerProfitType  int16 // 服务器利润（回退用）
	ServerProfitValue float64
}

// AdminServiceFilter 后台服务列表筛选。
type AdminServiceFilter struct {
	Keyword   string // 用户邮箱 / 服务名 / 产品名 模糊
	ProductID int64  // 0=全部
	Status    int16  // -1=全部；0 待开通 /1 激活 /2 已停机
}

// AdminList 后台服务列表（服务端筛选，LIMIT 200 保持）。
func (s *ServicesRepo) AdminList(ctx context.Context, f AdminServiceFilter) ([]AdminServiceRow, error) {
	query := `SELECT sv.id, coalesce(u.email,''), coalesce(sv.name,''),
	        CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
	        to_char(coalesce(sv.expires_at, sv.created_at),'YYYY-MM-DD'),
	        coalesce(sv.upstream_host_id::text,''), coalesce(sv.provision_error,''),
	        coalesce((SELECT profit FROM orders WHERE id=sv.order_id),'0'),
	        sv.product_id, coalesce(sv.hostname,''), coalesce(sv.expires_at, sv.created_at),
	        coalesce(sv.config_snapshot, o.config_snapshot),
	        coalesce(p.configoption::text,'[]'),
	        coalesce(pp.monthly::text,'0'),
	        p.profit_type, p.profit_value, coalesce(s.profit_type,0), coalesce(s.profit_value,0)
	 FROM services sv JOIN users u ON u.id=sv.user_id
	 JOIN products p ON p.id=sv.product_id
	 LEFT JOIN servers s ON s.id=p.server_id
	 LEFT JOIN orders o ON o.id=sv.order_id
	 LEFT JOIN product_prices pp ON pp.product_id=p.id AND pp.priceset_id=(SELECT min(id) FROM pricesets)
	 WHERE sv.status < 3`
	args := []any{}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		args = append(args, "%"+k+"%")
		query += fmt.Sprintf(` AND (u.email ILIKE $%d OR sv.name ILIKE $%d OR p.name ILIKE $%d)`, len(args), len(args), len(args))
	}
	if f.ProductID > 0 {
		args = append(args, f.ProductID)
		query += fmt.Sprintf(` AND sv.product_id=$%d`, len(args))
	}
	if f.Status >= 0 && f.Status <= 2 {
		args = append(args, f.Status)
		query += fmt.Sprintf(` AND sv.status=$%d`, len(args))
	}
	query += ` ORDER BY sv.id DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminServiceRow
	for rows.Next() {
		var r AdminServiceRow
		rows.Scan(&r.ID, &r.User, &r.Name, &r.Status, &r.Expires, &r.Upstream, &r.ProvErr, &r.Profit,
			&r.ProductID, &r.Hostname, &r.ExpiresAt,
			&r.ConfigSnap, &r.ConfigOpts, &r.MonthlyBase,
			&r.ProfitType, &r.ProfitValue, &r.ServerProfitType, &r.ServerProfitValue) // 单行失败不中断
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminServiceStatus 后台状态轮询行。
type AdminServiceStatus struct {
	ID          int64
	StatusLabel string
	ProvErr     string
}

// AdminStatus 后台服务状态轮询（仅读 DB）。
func (s *ServicesRepo) AdminStatus(ctx context.Context) ([]AdminServiceStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sv.id,
			CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
			coalesce(sv.provision_error,'')
		 FROM services sv WHERE sv.status < 3 ORDER BY sv.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminServiceStatus
	for rows.Next() {
		var st AdminServiceStatus
		if err := rows.Scan(&st.ID, &st.StatusLabel, &st.ProvErr); err != nil {
			break
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
