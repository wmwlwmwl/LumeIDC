package repo

import (
	"context"

	"golang.org/x/crypto/bcrypt"
)

type UserRow struct {
	ID    int64
	Email string
	Name  string
}

// ListUsers 后台用户列表（不含密码哈希）
func (u *Users) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := u.DB.QueryContext(ctx,
		`SELECT id,email,name FROM users ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetStatus 禁用/启用。
func (u *Users) SetStatus(ctx context.Context, userID int64, active bool) error {
	st := 1
	if !active {
		st = 0
	}
	_, err := u.DB.ExecContext(ctx,
		`UPDATE users SET status=$2 WHERE id=$1`, userID, st)
	return err
}

// ResetPassword 管理员重置密码。
func (u *Users) ResetPassword(ctx context.Context, userID int64, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = u.DB.ExecContext(ctx,
		`UPDATE users SET password_hash=$2 WHERE id=$1`, userID, string(hash))
	return err
}
