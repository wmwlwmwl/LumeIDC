package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LoginAttempts 登录失败计数与锁定（防爆破）。ponytail: 单表按 key（邮箱/IP）记录，
// 阈值后可换 Redis；多实例部署需共享存储。
type LoginAttempts struct{ db *sql.DB }

const (
	lockMaxAttempts = 5
	lockDuration    = 15 * time.Minute
)

// Locked 返回该 key 是否处于锁定中。
func (l *LoginAttempts) Locked(ctx context.Context, key string) (bool, error) {
	var until sql.NullTime
	err := l.db.QueryRowContext(ctx,
		`SELECT locked_until FROM login_attempts WHERE key=$1`, key).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return until.Valid && until.Time.After(time.Now()), nil
}

// Fail 记录一次失败；达到阈值则锁定一段时间。
// 注意 $3/$4 必须显式转型：CASE 的某个分支里只有参数和 NULL 时 PostgreSQL 无法推断参数类型，
// 会退化成 text，于是另一个分支出现「timestamptz 与 text 不匹配」(SQLSTATE 42804)，
// 整条语句直接失败。该错误曾被调用方用 `_ =` 吞掉，导致登录锁定长期完全不可用。
func (l *LoginAttempts) Fail(ctx context.Context, key string) error {
	now := time.Now()
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO login_attempts(key,attempts,first_at,locked_until)
		 VALUES($1,1,$2,NULL)
		 ON CONFLICT (key) DO UPDATE SET attempts=login_attempts.attempts+1,
		   locked_until = CASE WHEN login_attempts.attempts+1 >= $3::int THEN $4::timestamptz ELSE NULL END,
		   first_at = CASE WHEN login_attempts.attempts+1 >= $3::int THEN $4::timestamptz ELSE login_attempts.first_at END`,
		key, now, lockMaxAttempts, now.Add(lockDuration))
	return err
}

// Clear 登录成功后清除失败计数与锁定。
func (l *LoginAttempts) Clear(ctx context.Context, key string) error {
	_, err := l.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=$1`, key)
	return err
}

// Fails 返回该 key 当前的连续失败次数（无记录返回 0）。
func (l *LoginAttempts) Fails(ctx context.Context, key string) (int, error) {
	var n int
	err := l.db.QueryRowContext(ctx, `SELECT attempts FROM login_attempts WHERE key=$1`, key).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
