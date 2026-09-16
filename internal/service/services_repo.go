package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/server"
)

type ServiceRow struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Status       int16     `json:"status"`
	StatusText   string    `json:"-"`
	ExpiresAt    time.Time `json:"expires_at"`
	ProductID    int64     `json:"product_id"`
	ShowMonthly  bool      `json:"-"`
	ShowQ        bool      `json:"-"`
	ShowY        bool      `json:"-"`
	DefaultCycle string    `json:"-"`
	ExpiringSoon bool      `json:"-"` // 14 天内到期（列表提醒用）
	Hostname     string    `json:"hostname"`
	IP           string    `json:"ip"` // 上游实时（best-effort，列表展示）
	OS           string    `json:"os"` // 系统名称/版本（best-effort）
	Monthly      string    `json:"-"`  // 月售价（含配置+利润）
	ConfigDesc   string    `json:"-"`  // 配置摘要（如 "CPU 2核 · 内存 4G"）
	ConfigNote   string    `json:"-"`  // 后台手工填写的配置说明（非空时覆盖自动生成的摘要）
	DaysLeft     int       `json:"-"`  // 距到期天数
	// Transition 过渡状态（renew_pending=续费人工处理中）：前台据此提示并禁用再次续费。
	Transition  string `json:"-"`
	HasUpstream bool   `json:"-"`
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
		        coalesce(sv.config_snapshot, o.config_snapshot), coalesce(sv.config_desc,''),
		        coalesce(p.configoption::text,'[]'),
		        coalesce(pp.monthly::text,'0'), coalesce(pp.quarterly::text,'0'), coalesce(pp.yearly::text,'0'),
		        p.profit_type, p.profit_value, coalesce(s.profit_type,0), coalesce(s.profit_value,0),
		        coalesce(sv.transition_state,''),
		        (coalesce(sv.server_id,p.server_id) IS NOT NULL AND coalesce(sv.upstream_host_id,0)>0),
		        coalesce(sv.host_snapshot->'Detail'->>'IP',''),
		        coalesce(sv.host_snapshot->'Detail'->>'OSName',''),
		        coalesce(sv.host_snapshot->'Detail'->>'OSVersion','')
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
		var osVersion string
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Status, &sr.ExpiresAt, &sr.ProductID, &sr.Hostname,
			&sr.ConfigSnap, &sr.ConfigNote, &sr.ConfigOpts, &sr.MonthlyBase, &sr.QuarterlyBase, &sr.YearlyBase,
			&sr.ProfitType, &sr.ProfitValue, &sr.ServerProfitType, &sr.ServerProfitValue, &sr.Transition,
			&sr.HasUpstream, &sr.IP, &sr.OS, &osVersion); err != nil {
			return nil, err
		}
		if sr.OS != "" && osVersion != "" {
			sr.OS += "-" + osVersion
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
	ProductID int64     `json:"product_id"`
	Hostname  string    `json:"hostname"`
	CreatedAt time.Time `json:"created_at"`
	Cycle     string    `json:"cycle"`
	Amount    string    `json:"amount"`
	// RenewM/Q/Y 服务冻结的固定续费价（后台可改可清空，空=跟随产品当前价）。
	RenewM       string               `json:"renew_monthly"`
	RenewQ       string               `json:"renew_quarterly"`
	RenewY       string               `json:"renew_yearly"`
	Configs      []ConfigSnapshot     `json:"configs"`
	UpstreamHost int64                `json:"upstream_host_id"`
	Remark       string               `json:"remark"`
	ConfigNote   string               `json:"config_desc"` // 后台手工填写的配置说明（空=按产品配置项自动生成）
	Provider     string               `json:"provider"`    // 上游供应商代码（zjmf/easypanel…），前台按能力差异化展示
	HostSnapshot *server.HostOverview `json:"-"`
}

// GetDetail 读取服务详情（归属校验在 SQL 内）。
func (s *ServicesRepo) GetDetail(ctx context.Context, serviceID, userID int64) (*ServiceDetail, error) {
	var d ServiceDetail
	var orderID sql.NullInt64
	var snapshot []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT sv.id, sv.name, sv.status, coalesce(sv.expires_at,sv.created_at), sv.hostname,
		        sv.created_at, sv.upstream_host_id,
		        coalesce(sv.order_id, (SELECT o2.id FROM orders o2 WHERE o2.service_id=sv.id ORDER BY o2.id DESC LIMIT 1)),
		        sv.product_id, sv.remark, sv.host_snapshot,
		        coalesce(sv.config_desc,''),
		        coalesce(nullif(sv.upstream_provider,''),srv.provider,''),
		        coalesce(sv.renew_monthly::text,''), coalesce(sv.renew_quarterly::text,''), coalesce(sv.renew_yearly::text,''),
		        coalesce(sv.transition_state,'')
		 FROM services sv LEFT JOIN orders o ON o.id = sv.order_id
		 JOIN products p ON p.id = sv.product_id
		 LEFT JOIN servers srv ON srv.id = coalesce(sv.server_id, p.server_id)
		 WHERE sv.id=$1 AND sv.user_id=$2`, serviceID, userID).
		Scan(&d.ID, &d.Name, &d.Status, &d.ExpiresAt, &d.Hostname,
			&d.CreatedAt, &d.UpstreamHost, &orderID, &d.ProductID, &d.Remark, &snapshot, &d.ConfigNote, &d.Provider,
			&d.RenewM, &d.RenewQ, &d.RenewY, &d.Transition)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errSvcNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(snapshot) > 0 {
		var overview server.HostOverview
		if json.Unmarshal(snapshot, &overview) == nil {
			d.HostSnapshot = &overview
		}
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
			 FROM orders o LEFT JOIN services sv ON sv.id=$2 WHERE o.id=$1`,
			orderID.Int64, serviceID).Scan(&oCycle, &amount, &oSnap, &svCycle, &svSnap); err == nil {
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

// SaveHostSnapshot 保存最近一次用户主动刷新得到的上游概况。
func (s *ServicesRepo) SaveHostSnapshot(ctx context.Context, serviceID, userID int64, overview server.HostOverview) error {
	raw, err := json.Marshal(overview)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE services SET host_snapshot=$3 WHERE id=$1 AND user_id=$2`, serviceID, userID, raw)
	return err
}

// UpdateExpiryFromHost 更新服务到期时间，仅用于明确的上游刷新流程。
func (s *ServicesRepo) UpdateExpiryFromHost(ctx context.Context, serviceID, userID int64, expiry time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE services SET expires_at=$3 WHERE id=$1 AND user_id=$2`, serviceID, userID, expiry)
	return err
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

// UserStats 账户概览统计：服务数/激活数/累计消费/未支付账单数（SQL 与旧 handler 内查询逐字平移）。
// 累计消费＝已付订单账单 − 已退款额：充值只是余额入账不构成消费；退款把已付金额退回用户、
// 也不构成消费（退款不改账单状态，故必须单独扣减）。
// ponytail: 统计失败按零值降级（页面可用性优先），但记录日志便于排查。
func (s *ServicesRepo) UserStats(ctx context.Context, userID int64) (serviceCount, activeCount, unpaidCount int64, paidTotal string) {
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*),count(*) FILTER (WHERE status=1) FROM services WHERE user_id=$1 AND status<3`, userID).
		Scan(&serviceCount, &activeCount); err != nil {
		log.Printf("[repo] 用户 %d 服务统计查询失败: %v", userID, err)
	}
	// 累计消费：kind<>'recharge' 排除充值账单；再扣减 refunds（只由订单退款写入，
	// 后台"余额退款"走余额调整不落此表，故按 user_id 汇总不会误扣）。
	if err := s.db.QueryRowContext(ctx,
		`SELECT coalesce((SELECT sum(coalesce(paid_amount,amount)) FROM invoices
		                   WHERE user_id=$1 AND status=1 AND kind <> 'recharge'),0)
		      - coalesce((SELECT sum(amount::numeric) FROM refunds
		                   WHERE user_id=$1 AND status='done'),0)`, userID).
		Scan(&paidTotal); err != nil {
		log.Printf("[repo] 用户 %d 支付统计查询失败: %v", userID, err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM invoices WHERE user_id=$1 AND status=0`, userID).
		Scan(&unpaidCount); err != nil {
		log.Printf("[repo] 用户 %d 账单统计查询失败: %v", userID, err)
	}
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
	RecoveryRequired bool
	// RecoveryKind / RecoveryVersion 待对账隔离任务的类型与领取版本（无隔离任务时空串/0），
	// 前端据此渲染「对账恢复」入口并防并发提交。
	RecoveryKind    string
	RecoveryVersion int64
	ID              int64
	UserID          int64
	User            string // email
	Name            string
	Status          string
	Expires         string
	Upstream        string // upstream host id
	ProvErr         string
	// Transition 服务过渡状态（如 upgrading）：后台据此展示"升级中"的操作入口。
	Transition string
	Profit     string
	ProductID  int64
	Hostname   string
	ExpiresAt  time.Time
	// 管理员固定续费价覆盖（空串=跟随产品价）
	RenewM string
	RenewQ string
	RenewY string
	// 后台手工填写的配置说明（空串=按产品配置项自动生成）
	ConfigNote string
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
	query := `SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=sv.id AND recovery_required),
	        coalesce((SELECT min(kind) FROM fulfillment_jobs WHERE service_id=sv.id AND recovery_required),''),
	        coalesce((SELECT min(claim_version) FROM fulfillment_jobs WHERE service_id=sv.id AND recovery_required),0),
	        sv.id, sv.user_id, coalesce(u.email,''), coalesce(sv.name,''),
	        CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
	        to_char(coalesce(sv.expires_at, sv.created_at),'YYYY-MM-DD'),
	        coalesce(sv.upstream_host_id::text,''), coalesce(sv.provision_error,''), coalesce(sv.transition_state,''),
	        coalesce((SELECT profit FROM orders WHERE id=sv.order_id),'0'),
	        sv.product_id, coalesce(sv.hostname,''), coalesce(sv.expires_at, sv.created_at),
	        coalesce(sv.renew_monthly::text,''), coalesce(sv.renew_quarterly::text,''), coalesce(sv.renew_yearly::text,''),
	        coalesce(sv.config_desc,''),
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
		if err := rows.Scan(&r.RecoveryRequired, &r.RecoveryKind, &r.RecoveryVersion,
			&r.ID, &r.UserID, &r.User, &r.Name, &r.Status, &r.Expires, &r.Upstream, &r.ProvErr, &r.Transition, &r.Profit,
			&r.ProductID, &r.Hostname, &r.ExpiresAt,
			&r.RenewM, &r.RenewQ, &r.RenewY, &r.ConfigNote,
			&r.ConfigSnap, &r.ConfigOpts, &r.MonthlyBase,
			&r.ProfitType, &r.ProfitValue, &r.ServerProfitType, &r.ServerProfitValue); err != nil {
			// 单行失败跳过：零值行（ID=0、字段全空）混入列表会误导后台操作
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminServiceStatus 后台状态轮询行。
type AdminServiceStatus struct {
	ID          int64
	StatusLabel string
	ProvErr     string
	Transition  string
}

// AdminStatus 后台服务状态轮询（仅读 DB）。
func (s *ServicesRepo) AdminStatus(ctx context.Context) ([]AdminServiceStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sv.id,
			CASE sv.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
			coalesce(sv.provision_error,''), coalesce(sv.transition_state,'')
		 FROM services sv WHERE sv.status < 3 ORDER BY sv.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminServiceStatus
	for rows.Next() {
		var st AdminServiceStatus
		if err := rows.Scan(&st.ID, &st.StatusLabel, &st.ProvErr, &st.Transition); err != nil {
			break
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// AdminEdit 后台编辑服务：换归属用户 / 到期时间 / 固定续费价 / 配置说明。
// 各指针为 nil 表示不修改；renew 指向 0 表示清除覆盖（恢复跟随产品价）；
// configDesc 指向空串表示清除手工配置说明（恢复按产品配置项自动生成）。
// 返回更新后的归属用户 ID（供写服务日志——换用户后日志跟随新归属可见）。
func (s *ServicesRepo) AdminEdit(ctx context.Context, serviceID int64, userID *int64, expiresAt *time.Time, renewM, renewQ, renewY *float64, configDesc *string) (int64, error) {
	sets, args := []string{}, []any{}
	if userID != nil {
		args = append(args, *userID)
		sets = append(sets, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if expiresAt != nil {
		args = append(args, *expiresAt)
		sets = append(sets, fmt.Sprintf("expires_at=$%d", len(args)))
	}
	for _, p := range []struct {
		col string
		v   *float64
	}{{"renew_monthly", renewM}, {"renew_quarterly", renewQ}, {"renew_yearly", renewY}} {
		if p.v == nil {
			continue
		}
		args = append(args, sql.NullFloat64{Float64: *p.v, Valid: *p.v > 0})
		sets = append(sets, fmt.Sprintf("%s=$%d", p.col, len(args)))
	}
	if configDesc != nil {
		args = append(args, sql.NullString{String: *configDesc, Valid: *configDesc != ""})
		sets = append(sets, fmt.Sprintf("config_desc=$%d", len(args)))
	}
	if len(sets) > 0 {
		args = append(args, serviceID)
		tag, err := s.db.ExecContext(ctx,
			`UPDATE services SET `+strings.Join(sets, ",")+` WHERE id=$`+strconv.Itoa(len(args)), args...)
		if err != nil {
			return 0, err
		}
		if n, _ := tag.RowsAffected(); n == 0 {
			return 0, fmt.Errorf("服务不存在")
		}
	}
	var uid int64
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM services WHERE id=$1`, serviceID).Scan(&uid); err != nil {
		return 0, fmt.Errorf("服务不存在")
	}
	return uid, nil
}
