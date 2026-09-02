package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AuthChallenges stores one-time email/phone challenges. Plaintext codes are never persisted.
type AuthChallenges struct{ DB *sql.DB }

var ErrAuthChallengeInvalid = errors.New("验证码无效或已过期")
var ErrAuthChallengeCode = errors.New("验证码错误")

func (r *AuthChallenges) CreateAnonymous(ctx context.Context, channel, purpose, destination, destinationHMAC, codeHMAC, ip string, expiresAt, now time.Time) (int64, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE auth_challenges SET invalidated_at=$1 WHERE user_id IS NULL AND channel=$2 AND purpose=$3 AND destination_hmac=$4 AND consumed_at IS NULL AND invalidated_at IS NULL`, now, channel, purpose, destinationHMAC); err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO auth_challenges(user_id,channel,purpose,destination,destination_hmac,code_hmac,expires_at,request_ip,created_at) VALUES(NULL,$1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, channel, purpose, destination, destinationHMAC, codeHMAC, expiresAt, ip, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (r *AuthChallenges) RateLimited(ctx context.Context, channel, destinationHMAC, ip string, now time.Time) (bool, error) {
	var count int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM auth_challenges WHERE channel=$1 AND (destination_hmac=$2 OR request_ip=$3) AND created_at>$4`, channel, destinationHMAC, ip, now.Add(-time.Hour)).Scan(&count)
	return count >= 10, err
}
func (r *AuthChallenges) Invalidate(ctx context.Context, id int64, at time.Time) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE auth_challenges SET invalidated_at=$2 WHERE id=$1 AND consumed_at IS NULL`, id, at)
	return err
}

func (r *AuthChallenges) ConsumeAnonymous(ctx context.Context, channel, purpose, destinationHMAC, codeHMAC string, now time.Time) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	var saved string
	var expires time.Time
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT id,code_hmac,expires_at,attempts FROM auth_challenges WHERE user_id IS NULL AND channel=$1 AND purpose=$2 AND destination_hmac=$3 AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE`, channel, purpose, destinationHMAC).Scan(&id, &saved, &expires, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthChallengeInvalid
	}
	if err != nil {
		return err
	}
	if attempts >= 5 || !expires.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE auth_challenges SET invalidated_at=$2 WHERE id=$1`, id, now)
		_ = tx.Commit()
		return ErrAuthChallengeInvalid
	}
	if !secureEqual(saved, codeHMAC) {
		if attempts+1 >= 5 {
			_, _ = tx.ExecContext(ctx, `UPDATE auth_challenges SET attempts=attempts+1,invalidated_at=$2 WHERE id=$1`, id, now)
		} else {
			_, _ = tx.ExecContext(ctx, `UPDATE auth_challenges SET attempts=attempts+1 WHERE id=$1`, id)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrAuthChallengeCode
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_challenges SET consumed_at=$2 WHERE id=$1`, id, now); err != nil {
		return err
	}
	return tx.Commit()
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
