package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
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
}

type ServicesRepo struct{ db *sql.DB }

func (s *ServicesRepo) ListByUser(ctx context.Context, userID int64) ([]ServiceRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,status,coalesce(expires_at,created_at),product_id FROM services WHERE user_id=$1 AND status<3 ORDER BY id DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceRow
	for rows.Next() {
		var sr ServiceRow
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Status, &sr.ExpiresAt, &sr.ProductID); err != nil {
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
}

// GetDetail 读取服务详情（归属校验在 SQL 内）。
func (s *ServicesRepo) GetDetail(ctx context.Context, serviceID, userID int64) (*ServiceDetail, error) {
	var d ServiceDetail
	var orderID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT sv.id, sv.name, sv.status, coalesce(sv.expires_at,sv.created_at), sv.hostname,
		        sv.created_at, sv.upstream_host_id, o.id, sv.product_id
		 FROM services sv LEFT JOIN orders o ON o.id = sv.order_id
		 WHERE sv.id=$1 AND sv.user_id=$2`, serviceID, userID).
		Scan(&d.ID, &d.Name, &d.Status, &d.ExpiresAt, &d.Hostname,
			&d.CreatedAt, &d.UpstreamHost, &orderID, &d.ProductID)
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
	if orderID.Valid {
		var cycle, amount string
		var snap []byte
		if err := s.db.QueryRowContext(ctx,
			`SELECT cycle, amount, coalesce(config_snapshot::text,'') FROM orders WHERE id=$1`,
			orderID.Int64).Scan(&cycle, &amount, &snap); err == nil {
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

// AdminServiceRow 后台服务列表行（SQL 与旧 handler 查询逐字平移）。
type AdminServiceRow struct {
	ID       int64
	User     string // email
	Name     string
	Status   string
	Expires  string
	Upstream string // upstream host id
	ProvErr  string
	Profit   string
}

// AdminList 后台服务列表（最近 200 条）。
func (s *ServicesRepo) AdminList(ctx context.Context) ([]AdminServiceRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sv.id, coalesce(u.email,''), coalesce(sv.name,''),
			CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
			to_char(coalesce(sv.expires_at, sv.created_at),'YYYY-MM-DD'),
			sv.upstream_host_id, coalesce(sv.provision_error,''),
			coalesce((SELECT profit FROM orders WHERE id=sv.order_id),'0')
		 FROM services sv JOIN users u ON u.id=sv.user_id WHERE sv.status < 3 ORDER BY sv.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminServiceRow
	for rows.Next() {
		var r AdminServiceRow
		rows.Scan(&r.ID, &r.User, &r.Name, &r.Status, &r.Expires, &r.Upstream, &r.ProvErr, &r.Profit) // 与旧实现一致：单行失败不中断
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
