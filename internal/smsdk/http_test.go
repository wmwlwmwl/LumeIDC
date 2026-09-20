package smsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// nil client 兜底回归：路由/outbox 等无注入客户端的路径传 nil 不得 panic，
// 应走内置 15s 超时客户端完成请求。
func TestDoRequestNilClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()

	raw, err := DoRequest(context.Background(), nil, http.MethodPost, srv.URL, []byte(`{"a":"b"}`), map[string]string{"X-Test": "1"})
	if err != nil {
		t.Fatalf("nil client 请求失败: %v", err)
	}
	if string(raw) != `{"ok":1}` {
		t.Fatalf("响应不符: %s", raw)
	}
}

// 不跟随重定向（SSRF 防护）回归。
func TestDoRequestNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := DoRequest(context.Background(), nil, http.MethodGet, srv.URL, nil, nil)
	if err == nil {
		t.Fatal("重定向响应应被视为失败（结果未确认）")
	}
	if _, ok := err.(UnknownError); !ok {
		t.Fatalf("错误类型应为 UnknownError，实际 %T", err)
	}
}
