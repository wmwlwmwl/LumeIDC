package repo

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var ErrNotFound = errors.New("记录不存在")

type User struct {
	ID            int64
	Email         string
	Name          string
	Balance       string
	Verified      bool
	Phone         string
	PhoneVerified bool
}

type Users struct{ DB *sql.DB }

// NormalizeEmail 只接受纯邮箱地址并统一为小写，拒绝带显示名的地址。
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || email == "" || strings.ContainsAny(email, "\r\n") {
		return "", errors.New("邮箱格式不正确")
	}
	return email, nil
}

func (u *Users) Create(ctx context.Context, email, password, name string) (int64, error) {
	return u.CreateAccount(ctx, email, "", password, name, false)
}

// CreateAccount 创建邮箱或手机号账号；email/phone 至少一个非空由调用方保证。
func (u *Users) CreateAccount(ctx context.Context, email, phone, password, name string, phoneVerified bool) (int64, error) {
	if email == "" && phone == "" {
		return 0, errors.New("邮箱或手机号至少填写一项")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	var id int64
	err = u.DB.QueryRowContext(ctx,
		`INSERT INTO users(email,phone_e164,phone_verified_at,email_verified,password_hash,name) VALUES(NULLIF($1,''),NULLIF($2,''),CASE WHEN $5 THEN now() ELSE NULL END,CASE WHEN $1='' THEN true ELSE false END,$3,$4) RETURNING id`,
		email, phone, string(hash), name, phoneVerified).Scan(&id)
	return id, err
}

func (u *Users) ByEmail(ctx context.Context, email string) (*User, []byte, error) {
	if email == "" {
		return nil, nil, ErrNotFound
	}
	row := u.DB.QueryRowContext(ctx,
		`SELECT id,email,name,password_hash,status,email_verified,coalesce(phone_e164,''),phone_verified_at IS NOT NULL FROM users WHERE email=$1`, email)
	var usr User
	var hash []byte
	var status int16
	if err := row.Scan(&usr.ID, &usr.Email, &usr.Name, &hash, &status, &usr.Verified, &usr.Phone, &usr.PhoneVerified); err != nil {
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

// ByPhone 按手机号查询账户；密码登录始终支持，短信 OTP 登录由 handler 额外要求已验证。
func (u *Users) ByPhone(ctx context.Context, phone string) (*User, []byte, error) {
	row := u.DB.QueryRowContext(ctx,
		`SELECT id,email,name,password_hash,status,email_verified,coalesce(phone_e164,''),phone_verified_at IS NOT NULL FROM users WHERE phone_e164=$1`, phone)
	var usr User
	var hash []byte
	var status int16
	var email sql.NullString
	if err := row.Scan(&usr.ID, &email, &usr.Name, &hash, &status, &usr.Verified, &usr.Phone, &usr.PhoneVerified); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	if status == 0 {
		return nil, nil, ErrDisabled
	}
	usr.Email = email.String
	return &usr, hash, nil
}

// PasswordHash 返回用户密码哈希，供已登录的敏感换绑操作再次验证密码。
func (u *Users) PasswordHash(ctx context.Context, userID int64) ([]byte, error) {
	var hash []byte
	err := u.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1 AND status=1`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return hash, err
}

func (u *Users) MarkPhoneVerified(ctx context.Context, userID int64) error {
	_, err := u.DB.ExecContext(ctx, `UPDATE users SET phone_verified_at=now(),phone_updated_at=now() WHERE id=$1`, userID)
	return err
}

func (u *Users) SetVerifyToken(ctx context.Context, userID int64, token string) error {
	_, err := u.DB.ExecContext(ctx, `UPDATE users SET verify_token=$2,verify_token_created_at=now() WHERE id=$1`, userID, token)
	return err
}

// MarkVerified 直接标记已验证（无邮件能力时使用）。
func (u *Users) MarkVerified(ctx context.Context, userID int64) error {
	_, err := u.DB.ExecContext(ctx,
		`UPDATE users SET email_verified=true, verify_token='', verify_token_created_at=NULL WHERE id=$1`, userID)
	return err
}

// VerifyEmail 使用令牌完成验证；无效或已用返回错误。
func (u *Users) VerifyEmail(ctx context.Context, token string) error {
	res, err := u.DB.ExecContext(ctx,
		`UPDATE users SET email_verified=true, verify_token='', verify_token_created_at=NULL WHERE verify_token=$1 AND email_verified=false AND verify_token_created_at IS NOT NULL AND verify_token_created_at > now()-interval '24 hours'`, token)
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

// DeleteUnverified 删除尚未验证且刚创建失败的用户，避免邮箱被永久占用。
func (u *Users) DeleteUnverified(ctx context.Context, userID int64) error {
	_, err := u.DB.ExecContext(ctx, `DELETE FROM users WHERE id=$1 AND email_verified=false`, userID)
	return err
}
func (u *Users) TouchLogin(ctx context.Context, userID int64) error {
	_, err := u.DB.ExecContext(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	return err
}

func (u *Users) Count(ctx context.Context) (int64, error) {
	var n int64
	err := u.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

var ErrDisabled = errDisabled("账号已禁用")

type errDisabled string

func (e errDisabled) Error() string { return string(e) }
