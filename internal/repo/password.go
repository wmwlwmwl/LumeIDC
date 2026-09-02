package repo

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// ChangePassword 校验旧密码并更新为新密码哈希。
func (u *Users) ChangePassword(ctx context.Context, userID int64, oldPass, newPass string) error {
	var hash []byte
	err := u.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash)
	if err != nil {
		return errors.New("用户不存在")
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(oldPass)) != nil {
		return errors.New("旧密码错误")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = u.db.ExecContext(ctx,
		`UPDATE users SET password_hash=$2 WHERE id=$1`, userID, string(newHash))
	return err
}
