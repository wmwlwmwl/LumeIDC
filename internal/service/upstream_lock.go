package service

import (
	"context"
	"database/sql"
	"hash/fnv"
	"net/url"
	"strings"

	"lumeidc/internal/server"
)

// lockUpstreamAccount uses a PostgreSQL advisory lock for the legacy global
// shopping cart. The dedicated connection keeps the lock through all requests.
func lockUpstreamAccount(ctx context.Context, db *sql.DB, cfg server.Config) (func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	h := fnv.New64a()
	normalized := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	if u, err := url.Parse(normalized); err == nil {
		normalized = strings.ToLower(u.Scheme+"://"+u.Host) + strings.TrimRight(u.EscapedPath(), "/")
	}
	_, _ = h.Write([]byte(normalized + "\x00" + cfg.APIUsername))
	key := int64(h.Sum64())
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		_ = conn.Close()
	}, nil
}
