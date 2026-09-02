package repo

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// AdminLog 管理员操作审计日志。ponytail: 仅记录后台关键写操作，读操作不记。
type AdminLog struct{ db *sql.DB }

type AdminLogRow struct {
	ID         int64
	AdminID    int64
	Action     string
	TargetType string
	TargetID   int64
	Detail     string
	IP         string
	CreatedAt  time.Time
}

func (l *AdminLog) Add(ctx context.Context, adminID int64, action, targetType string, targetID int64, detail, ip string) error {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO admin_logs(admin_id,action,target_type,target_id,detail,ip)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		adminID, action, targetType, targetID, detail, ip)
	return err
}

func (l *AdminLog) List(ctx context.Context, limit int) ([]AdminLogRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT id,admin_id,action,target_type,target_id,detail,ip,created_at
		 FROM admin_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminLogRow
	for rows.Next() {
		var r AdminLogRow
		if err := rows.Scan(&r.ID, &r.AdminID, &r.Action, &r.TargetType, &r.TargetID, &r.Detail, &r.IP, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Record 写审计日志，失败仅记日志不阻断主流程（语义与原 RecordAudit 一致）。
func (l *AdminLog) Record(adminID int64, action, targetType string, targetID int64, detail, ip string) {
	if l == nil {
		return
	}
	if err := l.Add(context.Background(), adminID, action, targetType, targetID, detail, ip); err != nil {
		log.Printf("[audit] 写入失败: %v", err)
	}
}
