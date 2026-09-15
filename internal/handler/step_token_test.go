package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestStepToken(t *testing.T) {
	secret := []byte("test-secret")
	uid := int64(42)

	tok, err := issueStepToken(secret, uid, "email-change")
	if err != nil {
		t.Fatalf("issueStepToken: %v", err)
	}
	if !verifyStepToken(secret, tok, uid, "email-change") {
		t.Fatal("合法 token 校验失败")
	}

	cases := []struct {
		name      string
		token     string
		userID    int64
		purpose   string
		wantValid bool
	}{
		{"目的不符", tok, uid, "phone-change", false},
		{"用户不符", tok, 7, "email-change", false},
		{"密钥不符", mustToken(t, []byte("other"), uid, "email-change"), uid, "email-change", false},
		{"签名被篡改", tamper(tok), uid, "email-change", false},
		{"空 token", "", uid, "email-change", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verifyStepToken(secret, c.token, c.userID, c.purpose); got != c.wantValid {
				t.Fatalf("verifyStepToken = %v, want %v", got, c.wantValid)
			}
		})
	}
}

func TestStepTokenExpired(t *testing.T) {
	secret := []byte("test-secret")
	exp := time.Now().Add(-time.Minute).Unix()
	msg := fmt.Sprintf("%d|%s|%d", 1, "email-change", exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	token := base64.RawURLEncoding.EncodeToString([]byte(msg + "|" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))))
	if verifyStepToken(secret, token, 1, "email-change") {
		t.Fatal("过期 token 不应通过校验")
	}
}

func mustToken(t *testing.T, secret []byte, uid int64, purpose string) string {
	t.Helper()
	tok, err := issueStepToken(secret, uid, purpose)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func tamper(s string) string {
	buf := []byte(s)
	buf[len(buf)-1] ^= 0x01
	return string(buf)
}