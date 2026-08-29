package totp

import (
	"testing"
	"time"
)

func TestTOTPGenerateAndVerify(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	now := time.Now()
	code, err := Generate(secret, now)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code 长度应为 6，实际 %q", code)
	}
	if !Verify(secret, code, now) {
		t.Fatal("当前窗口应校验通过")
	}
	// 容错：±1 窗口
	if !Verify(secret, code, now.Add(30*time.Second)) {
		t.Fatal("+1 窗口应校验通过")
	}
	if Verify(secret, "000000", now) && "000000" != code {
		t.Fatal("错误码不应通过")
	}
}
