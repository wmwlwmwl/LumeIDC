package repo

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var ErrNotFound = errors.New("记录不存在")

type User struct {
	ID       int64
	Email    string
	Name     string
	Balance  string
	Verified bool
}

type Users struct{ DB *sql.DB }

func (u *Users) Create(ctx context.Context, email, password, name string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	var id int64
	err = u.DB.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash,name) VALUES($1,$2,$3) RETURNING id`,
		email, string(hash), name).Scan(&id)
	return id, err
}

func (u *Users) ByEmail(ctx context.Context, email string) (*User, []byte, error) {
	row := u.DB.QueryRowContext(ctx,
		`SELECT id,email,name,password_hash,status,email_verified FROM users WHERE email=$1`, email)
	var usr User
	var hash []byte
	var status int16
	if err := row.Scan(&usr.ID, &usr.Email, &usr.Name, &hash, &status, &usr.Verified); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	if status == 0 {
		return nil, nil, ErrDisabled
	}
	return &usr, hash, nil
}

// SetVerifyToken 写入邮箱验证令牌。
func (u *Users) SetVerifyToken(ctx context.Context, userID int64, token string) error {
	_, err := u.DB.ExecContext(ctx, `UPDATE users SET verify_token=$2 WHERE id=$1`, userID, token)
	return err
}

// MarkVerified 直接标记已验证（无邮件能力时使用）。
func (u *Users) MarkVerified(ctx context.Context, userID int64) error {
	_, err := u.DB.ExecContext(ctx,
		`UPDATE users SET email_verified=true, verify_token='' WHERE id=$1`, userID)
	return err
}

// VerifyEmail 使用令牌完成验证；无效或已用返回错误。
func (u *Users) VerifyEmail(ctx context.Context, token string) error {
	res, err := u.DB.ExecContext(ctx,
		`UPDATE users SET email_verified=true, verify_token='' WHERE verify_token=$1 AND email_verified=false`, token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("验证链接无效或已使用")
	}
	return nil
}

func (u *Users) VerifyPassword(hash []byte, password string) bool {
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}

func (u *Users) Count(ctx context.Context) (int64, error) {
	var n int64
	err := u.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

var ErrDisabled = errDisabled("账号已禁用")

type errDisabled string

func (e errDisabled) Error() string { return string(e) }
