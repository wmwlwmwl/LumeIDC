// Package totp 实现 RFC 6238 基于时间的一次性密码（TOTP），仅依赖标准库。
// ponytail: 30s 步长、6 位、SHA1，兼容 Google Authenticator / 1Password 等。
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"strconv"
	"strings"
	"time"
)

const step = 30 * time.Second

// NewSecret 生成 20 字节随机密钥（base32 无填充字符串）。
func NewSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// Generate 计算给定时间窗口的 6 位码。
func Generate(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / uint64(step.Seconds())
	return hotp(key, counter), nil
}

// Verify 校验码，允许 ±1 个窗口的时钟漂移。
func Verify(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return false
	}
	for d := -1; d <= 1; d++ {
		cand, err := Generate(secret, t.Add(time.Duration(d)*step))
		if err == nil && hmac.Equal([]byte(cand), []byte(code)) {
			return true
		}
	}
	return false
}

// URI 生成 otpauth:// URI，便于生成二维码。
func URI(label, issuer, secret string) string {
	return "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + issuer + "&period=30&digits=6&algorithm=SHA1"
}

func hotp(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1000000
	return strconv.FormatInt(int64(code), 10)
}
