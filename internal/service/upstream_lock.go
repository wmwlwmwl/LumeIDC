package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"hash/fnv"
	"net/url"
	"strings"
	"time"

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
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil || !locked {
		conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, &server.RetryLaterError{Msg: "上游账户正在处理其他开通，请稍后重试"}
	}
	// 解锁：advisory lock 是会话级的，解锁失败时若把连接还回池，锁会被这条池连接
	// 一直持有——该上游账户的后续开通将永远拿到「正在处理其他开通」而彻底卡死。
	// 因此解锁失败必须让连接作废（ErrBadConn 会让 database/sql 真正关掉会话，
	// 会话断开即由 PostgreSQL 释放锁），与 fulfillment 的服务锁同一套做法。
	// 顺带加上超时：DB 挂起时不能无限期占着履约执行槽。
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}
