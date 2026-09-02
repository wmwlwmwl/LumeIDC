package service

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestExternalCaptchaSDKAllowlist(t *testing.T) {
	cases := []struct{ provider, want string }{
		{"geetest", "https://static.geetest.com/v4/gt4.js"},
		{"vaptcha", "https://cdn4.vaptcha.com/src/v4.js"},
		{"corptcha", "https://res.25y.cn/corptcha/corptcha.iife.js"},
	}
	for _, tc := range cases {
		got, version := captchaSDK(tc.provider)
		if got != tc.want || version == "" {
			t.Fatalf("captchaSDK(%q) = %q, %q", tc.provider, got, version)
		}
	}
	if got, _ := captchaSDK("unknown"); got != "" {
		t.Fatalf("unknown SDK URL = %q", got)
	}
}

func TestExternalCaptchaResponseLimit(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), maxCaptchaResponseBytes+1)))}
	if _, err := readCaptchaResponse(resp); err == nil {
		t.Fatal("oversized response should fail")
	}
}
