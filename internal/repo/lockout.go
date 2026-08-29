package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LoginAttempts 登录失败计数与锁定（防爆破）。ponytail: 单表按 key（邮箱/IP）记录，
// 阈值后可换 Redis；多实例部署需共享存储。
type LoginAttempts struct{ DB *sql.DB }

const (
	lockMaxAttempts = 5
	lockDuration    = 15 * time.Minute
)

// Locked 返回该 key 是否处于锁定中。
func (l *LoginAttempts) Locked(ctx context.Context, key string) (bool, error) {
	var until sql.NullTime
	err := l.DB.QueryRowContext(ctx,
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
func (l *LoginAttempts) Fail(ctx context.Context, key string) error {
	now := time.Now()
	_, err := l.DB.ExecContext(ctx,
		`INSERT INTO login_attempts(key,attempts,first_at,locked_until)
		 VALUES($1,1,$2,NULL)
		 ON CONFLICT (key) DO UPDATE SET attempts=login_attempts.attempts+1,
		   locked_until = CASE WHEN login_attempts.attempts+1 >= $3 THEN $4 ELSE NULL END,
		   first_at = CASE WHEN login_attempts.attempts+1 >= $3 THEN $4 ELSE login_attempts.first_at END`,
		key, now, lockMaxAttempts, now.Add(lockDuration))
	return err
}

// Clear 登录成功后清除失败计数与锁定。
func (l *LoginAttempts) Clear(ctx context.Context, key string) error {
	_, err := l.DB.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=$1`, key)
	return err
}
