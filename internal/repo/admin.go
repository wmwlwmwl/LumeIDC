package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type Admins struct{ db *sql.DB }

// Create inserts admin with bcrypt hash; used by installer only.
func (a *Admins) Create(ctx context.Context, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx,
		`INSERT INTO admin_users(username,password_hash) VALUES($1,$2)`, username, string(hash))
	return err
}

func (a *Admins) Verify(ctx context.Context, username, password string) (int64, error) {
	var id int64
	var hash []byte
	err := a.db.QueryRowContext(ctx,
		`SELECT id,password_hash FROM admin_users WHERE username=$1`, username).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return 0, ErrNotFound // 不区分"用户不存在/密码错误"
	}
	return id, nil
}

// ChangePassword 管理员修改自己密码：先验证旧密码，新密码 bcrypt 落库。
func (a *Admins) ChangePassword(ctx context.Context, adminID int64, oldPassword, newPassword string) error {
	var hash []byte
	err := a.db.QueryRowContext(ctx,
		`SELECT password_hash FROM admin_users WHERE id=$1`, adminID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(oldPassword)) != nil {
		return errWrongOldPassword
	}
	if len(newPassword) < 8 {
		return errPasswordTooShort
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx,
		`UPDATE admin_users SET password_hash=$2 WHERE id=$1`, adminID, string(newHash))
	return err
}

var errWrongOldPassword = errors.New("旧密码错误")
var errPasswordTooShort = errors.New("新密码至少8位")
var errUsernameEmpty = errors.New("用户名不能为空")
var errUsernameTaken = errors.New("用户名已被占用")

// IsWrongOldPassword / IsPasswordTooShort 供 handler 转成用户可读提示。
func IsWrongOldPassword(err error) bool { return err == errWrongOldPassword }
func IsPasswordTooShort(err error) bool { return err == errPasswordTooShort }

// IsUsernameEmpty / IsUsernameTaken 同上。
func IsUsernameEmpty(err error) bool { return err == errUsernameEmpty }
func IsUsernameTaken(err error) bool { return err == errUsernameTaken }

// Username 读取管理员登录名（后台账户设置展示用）。
func (a *Admins) Username(ctx context.Context, adminID int64) (string, error) {
	var name string
	err := a.db.QueryRowContext(ctx, `SELECT username FROM admin_users WHERE id=$1`, adminID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// ChangeUsername 修改管理员登录名：先验证当前密码，再检查唯一性。
func (a *Admins) ChangeUsername(ctx context.Context, adminID int64, password, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return errUsernameEmpty
	}
	var hash []byte
	err := a.db.QueryRowContext(ctx,
		`SELECT password_hash FROM admin_users WHERE id=$1`, adminID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return errWrongOldPassword
	}
	// 预检唯一性，给出明确提示（比直接撞 UNIQUE 约束更友好）
	var taken bool
	if err := a.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM admin_users WHERE username=$1 AND id<>$2)`,
		newUsername, adminID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return errUsernameTaken
	}
	_, err = a.db.ExecContext(ctx,
		`UPDATE admin_users SET username=$2 WHERE id=$1`, adminID, newUsername)
	return err
}

// GetTOTP 读取管理员的 TOTP 配置。
func (a *Admins) GetTOTP(ctx context.Context, adminID int64) (secret string, enabled bool, err error) {
	err = a.db.QueryRowContext(ctx,
		`SELECT totp_secret, totp_enabled FROM admin_users WHERE id=$1`, adminID).
		Scan(&secret, &enabled)
	return
}

// SetTOTP 写入密钥并关闭启用态（待用户校验通过后启用）。
func (a *Admins) SetTOTP(ctx context.Context, adminID int64, secret string) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE admin_users SET totp_secret=$2, totp_enabled=false WHERE id=$1`, adminID, secret)
	return err
}

// EnableTOTP 启用两步验证。
func (a *Admins) EnableTOTP(ctx context.Context, adminID int64) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE admin_users SET totp_enabled=true WHERE id=$1 AND totp_secret<>''`, adminID)
	return err
}

// DisableTOTP 关闭两步验证并清除密钥。
func (a *Admins) DisableTOTP(ctx context.Context, adminID int64) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE admin_users SET totp_enabled=false, totp_secret='' WHERE id=$1`, adminID)
	return err
}
