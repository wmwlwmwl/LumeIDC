package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CancelRequests 用户「停用申请」（对齐魔方财务 cancel_requests）。
type CancelRequests struct{ db *sql.DB }

func NewCancelRequests(db *sql.DB) *CancelRequests { return &CancelRequests{db: db} }

// ErrCancelRequestExists 同一服务已有待处理申请。
var ErrCancelRequestExists = errors.New("该服务已存在待处理的停用申请")

type CancelRequest struct {
	ID           int64
	ServiceID    int64
	UserID       int64
	Type         string // Immediate / Endofbilling
	Reason       string
	ReasonDetail string
	Status       string // pending/approved/rejected/withdrawn
	HandleMode   string // local/upstream
	HandleNote   string
	HandledBy    sql.NullInt64
	HandledAt    sql.NullTime
	CreatedAt    time.Time
}

// CancelRequestAdminRow 后台列表行（附带服务/用户信息）。
type CancelRequestAdminRow struct {
	CancelRequest
	Username    string
	UserEmail   string
	ServiceName string
	ProductName string
	Hostname    string
	StatusText  string
	ExpiresAt   sql.NullTime
}

const cancelRequestCols = `id,service_id,user_id,type,reason,reason_detail,status,handle_mode,handle_note,handled_by,handled_at,created_at`

func scanCancelRequest(row interface{ Scan(...any) error }) (*CancelRequest, error) {
	var c CancelRequest
	if err := row.Scan(&c.ID, &c.ServiceID, &c.UserID, &c.Type, &c.Reason, &c.ReasonDetail,
		&c.Status, &c.HandleMode, &c.HandleNote, &c.HandledBy, &c.HandledAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// Create 新建停用申请；同一服务已有 pending 时返回 ErrCancelRequestExists。
func (r *CancelRequests) Create(ctx context.Context, serviceID, userID int64, typ, reason, detail string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO service_cancel_requests(service_id,user_id,type,reason,reason_detail)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (service_id) WHERE status='pending' DO NOTHING
		 RETURNING id`,
		serviceID, userID, typ, reason, detail).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrCancelRequestExists
	}
	return id, err
}

// ActiveByService 该服务当前的待处理申请（无则 nil）。
func (r *CancelRequests) ActiveByService(ctx context.Context, serviceID int64) (*CancelRequest, error) {
	c, err := scanCancelRequest(r.db.QueryRowContext(ctx,
		`SELECT `+cancelRequestCols+` FROM service_cancel_requests
		 WHERE service_id=$1 AND status='pending' ORDER BY id DESC LIMIT 1`, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// Get 按 ID 取申请。
func (r *CancelRequests) Get(ctx context.Context, id int64) (*CancelRequest, error) {
	c, err := scanCancelRequest(r.db.QueryRowContext(ctx,
		`SELECT `+cancelRequestCols+` FROM service_cancel_requests WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// Withdraw 用户撤回自己的待处理申请。
func (r *CancelRequests) Withdraw(ctx context.Context, id, userID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE service_cancel_requests SET status='withdrawn'
		 WHERE id=$1 AND user_id=$2 AND status='pending'`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AdminList 后台列表（status 为空则全部；附服务/用户信息）。
func (r *CancelRequests) AdminList(ctx context.Context, status string) ([]CancelRequestAdminRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT c.id,c.service_id,c.user_id,c.type,c.reason,c.reason_detail,c.status,
		        c.handle_mode,c.handle_note,c.handled_by,c.handled_at,c.created_at,
		        coalesce(nullif(u.name,''),u.email), u.email,
		        s.name, coalesce(p.name,''), s.hostname,
		        CASE s.status WHEN 0 THEN '待开通' WHEN 1 THEN '激活' WHEN 2 THEN '已停机' ELSE '已删除' END,
		        s.expires_at
		 FROM service_cancel_requests c
		 JOIN services s ON s.id=c.service_id
		 JOIN users u ON u.id=c.user_id
		 LEFT JOIN products p ON p.id=s.product_id
		 WHERE ($1='' OR c.status=$1)
		 ORDER BY c.id DESC LIMIT 300`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CancelRequestAdminRow, 0)
	for rows.Next() {
		var c CancelRequestAdminRow
		if err := rows.Scan(&c.ID, &c.ServiceID, &c.UserID, &c.Type, &c.Reason, &c.ReasonDetail,
			&c.Status, &c.HandleMode, &c.HandleNote, &c.HandledBy, &c.HandledAt, &c.CreatedAt,
			&c.Username, &c.UserEmail, &c.ServiceName, &c.ProductName, &c.Hostname,
			&c.StatusText, &c.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PendingCount 待处理申请数（后台角标）。
func (r *CancelRequests) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM service_cancel_requests WHERE status='pending'`).Scan(&n)
	return n, err
}

// MarkHandled 管理员处理申请：status=approved/rejected，记录处理方式/备注/处理人。
// 仅当申请仍为 pending 时生效（原子占位），返回是否占位成功——
// 并发处理同一申请时只有一个调用方拿到 true。
func (r *CancelRequests) MarkHandled(ctx context.Context, id int64, status, mode, note string, adminID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE service_cancel_requests
		 SET status=$2, handle_mode=$3, handle_note=$4, handled_by=$5, handled_at=now()
		 WHERE id=$1 AND status='pending'`, id, status, mode, note, adminID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Reopen 删除执行失败时把已占位的申请回滚为待处理（清除处理痕迹），供管理员重试。
func (r *CancelRequests) Reopen(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE service_cancel_requests
		 SET status='pending', handle_mode='', handle_note='', handled_by=NULL, handled_at=NULL
		 WHERE id=$1 AND status='approved'`, id)
	return err
}
