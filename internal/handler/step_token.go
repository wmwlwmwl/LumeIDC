package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// stepTokenLifetime 两步换绑的第一步通过后，第二步须在该期限内完成。
const stepTokenLifetime = 10 * time.Minute

// issueStepToken 为两步换绑签发无状态中间凭证（绑定 userID 与目的，HMAC 自校验）。
func issueStepToken(secret []byte, userID int64, purpose string) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("安全配置缺失")
	}
	exp := time.Now().Add(stepTokenLifetime).Unix()
	msg := fmt.Sprintf("%d|%s|%d", userID, purpose, exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(msg + "|" + sig)), nil
}

// verifyStepToken 校验中间凭证：签名、有效期、绑定对象。失效或错误返回 false。
func verifyStepToken(secret []byte, token string, userID int64, purpose string) bool {
	if token == "" || len(secret) == 0 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return false
	}
	msg, sig := parts[0]+"|"+parts[1]+"|"+parts[2], parts[3]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	got := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(got), []byte(sig)) {
		return false
	}
	if parts[1] != purpose {
		return false
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || uid != userID {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}