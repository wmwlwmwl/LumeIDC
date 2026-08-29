package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

func adminRequire(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

const requestTimeout = 15 * time.Second

func serverConfig(sv *repo.Server) server.Config {
	return server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
}

func sqlNull(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id > 0}
}

func money(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// findProductByUpstream 按 (server_id, upstream_pid) 反查已对接的本地产品 id；未找到返回 0。
func findProductByUpstream(ctx context.Context, db *sql.DB, serverID, upstreamPID int64) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM products WHERE server_id=$1 AND upstream_pid=$2 LIMIT 1`, serverID, upstreamPID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// linkedUpstreamPIDs 返回该上游服务器已对接的上游 PID 集合，用于目录页标注"已对接"。
func linkedUpstreamPIDs(ctx context.Context, db *sql.DB, serverID int64) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT upstream_pid FROM products WHERE server_id=$1 AND upstream_pid>0`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out[pid] = true
	}
	return out, rows.Err()
}
