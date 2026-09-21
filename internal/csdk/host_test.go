package csdk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeSettings map[string]string

func (f fakeSettings) Get(ctx context.Context, key string) (string, error) { return f[key], nil }

func TestHostGet(t *testing.T) {
	var nilHost *Host
	if got := nilHost.Get(context.Background(), "k"); got != "" {
		t.Fatalf("nil host 应返回空串, got %q", got)
	}
	h := &Host{Settings: fakeSettings{"k": "  v  "}}
	if got := h.Get(context.Background(), "k"); got != "v" {
		t.Fatalf("Get 应去空白, got %q", got)
	}
	if got := h.Get(context.Background(), "missing"); got != "" {
		t.Fatalf("缺失键应返回空串, got %q", got)
	}
}

func TestHostHTTPClientFallback(t *testing.T) {
	h := &Host{}
	c := h.HTTPClient()
	if c == nil || c.Timeout != 10*time.Second {
		t.Fatalf("nil client 应兜底 10s 超时, got %+v", c)
	}
	custom := &http.Client{Timeout: 3 * time.Second}
	h2 := &Host{Client: custom}
	if h2.HTTPClient() != custom {
		t.Fatal("注入的客户端应原样返回")
	}
}

func TestReadResponse(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"ok":1}`))}
	data, err := ReadResponse(resp)
	if err != nil || string(data) != `{"ok":1}` {
		t.Fatalf("正常响应读取失败: %v %q", err, data)
	}
	big := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", MaxResponseBytes+10)))}
	if _, err := ReadResponse(big); err == nil {
		t.Fatal("超限响应应报错")
	}
}

func TestHexString(t *testing.T) {
	if got := HexString([]byte{0x00, 0xff, 0x1a}); got != "00ff1a" {
		t.Fatalf("HexString 不符: %q", got)
	}
	if got := HexString(nil); got != "" {
		t.Fatalf("空输入应得空串: %q", got)
	}
}
