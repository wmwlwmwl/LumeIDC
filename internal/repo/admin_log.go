package repo

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// AdminLog 管理员操作审计日志。ponytail: 仅记录后台关键写操作，读操作不记。
type AdminLog struct{ DB *sql.DB }

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
	_, err := l.DB.ExecContext(ctx,
		`INSERT INTO admin_logs(admin_id,action,target_type,target_id,detail,ip)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		adminID, action, targetType, targetID, detail, ip)
	return err
}

func (l *AdminLog) List(ctx context.Context, limit int) ([]AdminLogRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := l.DB.QueryContext(ctx,
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

// RecordAudit 写审计日志，失败仅记日志不阻断主流程。
func RecordAudit(db *sql.DB, adminID int64, action, targetType string, targetID int64, detail, ip string) {
	if db == nil {
		return
	}
	if err := (&AdminLog{DB: db}).Add(context.Background(), adminID, action, targetType, targetID, detail, ip); err != nil {
		log.Printf("[audit] 写入失败: %v", err)
	}
}
