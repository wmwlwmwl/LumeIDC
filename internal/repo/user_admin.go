package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type UserRow struct {
	ID        int64
	Email     string
	Name      string
	Phone     string
	Status    int16
	Balance   float64
	CreatedAt string // 已格式化 'YYYY-MM-DD HH24:MI'
}

type AdminUser struct {
	ID            int64
	Email         string
	Name          string
	Status        int16
	Balance       float64
	Phone         string
	EmailVerified bool
	PhoneVerified bool
	CreatedAt     time.Time
	LastLoginAt   sql.NullTime
}

// ListUsers 后台用户列表（不含密码哈希）
func (u *Users) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := u.db.QueryContext(ctx,
		`SELECT id,coalesce(email,''),name,coalesce(phone_e164,''),status,balance::float8,
		        to_char(created_at,'YYYY-MM-DD HH24:MI')
		 FROM users ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Name, &r.Phone, &r.Status, &r.Balance, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminUserByID 读取后台用户编辑所需的最小字段。
func (u *Users) AdminUserByID(ctx context.Context, userID int64) (*AdminUser, error) {
	row := u.db.QueryRowContext(ctx, `
		SELECT id,coalesce(email,''),name,status,balance::float8,coalesce(phone_e164,''),email_verified,
		       phone_verified_at IS NOT NULL, created_at, last_login_at
		FROM users WHERE id=$1`, userID)
	var user AdminUser
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Status, &user.Balance,
		&user.Phone, &user.EmailVerified, &user.PhoneVerified, &user.CreatedAt, &user.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

// EmailTaken 检查邮箱是否被其他用户占用。
func (u *Users) EmailTaken(ctx context.Context, email string, excludeID int64) (bool, error) {
	var taken bool
	err := u.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email=$1 AND id<>$2)`, email, excludeID).Scan(&taken)
	return taken, err
}

// UpdateEmail 后台修改邮箱；邮箱实际变化时必须重新完成验证。
func (u *Users) UpdateEmail(ctx context.Context, userID int64, email string) error {
	_, err := u.db.ExecContext(ctx, `
		UPDATE users SET email=NULLIF($2,''),email_verified=false,verify_token=''
		WHERE id=$1 AND COALESCE(email,'')<>$2`, userID, email)
	return err
}

// SetStatus 禁用/启用。
func (u *Users) SetStatus(ctx context.Context, userID int64, active bool) error {
	st := 1
	if !active {
		st = 0
	}
	_, err := u.db.ExecContext(ctx,
		`UPDATE users SET status=$2 WHERE id=$1`, userID, st)
	return err
}

// ResetPassword 管理员重置密码。
func (u *Users) ResetPassword(ctx context.Context, userID int64, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = u.db.ExecContext(ctx,
		`UPDATE users SET password_hash=$2 WHERE id=$1`, userID, string(hash))
	return err
}
