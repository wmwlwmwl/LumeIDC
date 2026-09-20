package vsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSettings map[string]string

func (s fakeSettings) Get(_ context.Context, key string) (string, error) { return s[key], nil }

// Endpoint 白名单：https 强制、host 白名单、拒绝用户态/纯 IP 之外的地址。
func TestEndpointAllowlist(t *testing.T) {
	h := &Host{Settings: fakeSettings{}}

	// fallback 命中白名单
	got, err := h.Endpoint(context.Background(), "missing_key", "https://api.example.com/v1/", "api.example.com")
	if err != nil || got != "https://api.example.com/v1" {
		t.Fatalf("fallback 应命中白名单并去尾斜杠: %q %v", got, err)
	}

	// 设置项覆盖，host 在白名单内
	h2 := &Host{Settings: fakeSettings{"k": "https://api.example.com/custom"}}
	if got, err := h2.Endpoint(context.Background(), "k", "", "api.example.com"); err != nil || got != "https://api.example.com/custom" {
		t.Fatalf("设置项覆盖失败: %q %v", got, err)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"非 https", "http://api.example.com"},
		{"带用户态", "https://u:p@api.example.com"},
		{"host 不在白名单", "https://evil.example.com"},
		{"无 host", "https:///path"},
		{"非法地址", "://bad"},
	}
	for _, tc := range cases {
		hs := &Host{Settings: fakeSettings{"k": tc.raw}}
		if _, err := hs.Endpoint(context.Background(), "k", "", "api.example.com"); err == nil {
			t.Errorf("%s 应被拒绝: %s", tc.name, tc.raw)
		}
	}
}

// nil Client 兜底：不 panic，走内置 15s 超时客户端完成请求。
func TestRequestJSONNilClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %s, want application/json", ct)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out map[string]any
	h := &Host{Settings: fakeSettings{}}
	if err := h.RequestJSON(context.Background(), http.MethodPost, srv.URL, []byte(`{"a":1}`), nil, &out); err != nil {
		t.Fatalf("nil client 请求失败: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("响应解析失败: %v", out)
	}
}

// 非 2xx / 空响应 / 非 JSON 均报错。
func TestRequestJSONFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	h := &Host{Settings: fakeSettings{}}
	var out map[string]any
	if err := h.RequestJSON(context.Background(), http.MethodGet, srv.URL, nil, nil, &out); err == nil {
		t.Fatal("非 2xx 应报错")
	}
}

// RequestForm 表单编码与响应解析。
func TestRequestForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("action") != "query" {
			t.Error("表单字段丢失")
		}
		if r.Header.Get("X-App-Key") != "k1" {
			t.Error("自定义头丢失")
		}
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	h := &Host{Settings: fakeSettings{}}
	var out map[string]any
	err := h.RequestForm(context.Background(), srv.URL, map[string][]string{"action": {"query"}}, map[string]string{"X-App-Key": "k1"}, &out)
	if err != nil || out["status"] != "success" {
		t.Fatalf("RequestForm 失败: %v %v", err, out)
	}
}

// Get nil 宿主兜底。
func TestHostGetNilSafe(t *testing.T) {
	var h *Host
	if got := h.Get(context.Background(), "any"); got != "" {
		t.Fatalf("nil Host.Get 应返回空串，得到 %q", got)
	}
}

// StatusFromMap 状态归一化（含嵌套 result）。
func TestStatusFromMap(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"status": "approved"}, "approved"},
		{map[string]any{"verify_status": float64(1)}, "approved"},
		{map[string]any{"conclusion": "FAILED"}, "rejected"},
		{map[string]any{"result": map[string]any{"verify_result": "passed"}}, "approved"},
		{map[string]any{"status": "unknown_value"}, "pending"},
		{map[string]any{}, "pending"},
	}
	for i, tc := range cases {
		if got := StatusFromMap(tc.in).Status; got != tc.want {
			t.Errorf("用例 %d: got %q want %q", i, got, tc.want)
		}
	}
}

// ResponseOK 成功判定。
func TestResponseOK(t *testing.T) {
	if !ResponseOK(map[string]any{"code": float64(200)}) || !ResponseOK(map[string]any{"status": "ok"}) || !ResponseOK(map[string]any{"success": true}) {
		t.Error("成功判定漏报")
	}
	if ResponseOK(map[string]any{"code": float64(500)}) || ResponseOK(map[string]any{}) || ResponseOK(nil) {
		t.Error("失败判定误报")
	}
}

// SHA256Hex / RandomHex / FirstString 基础行为。
func TestMiscHelpers(t *testing.T) {
	if len(SHA256Hex([]byte("x"))) != 64 {
		t.Error("SHA256Hex 长度错误")
	}
	if got := RandomHex(8); len(got) != 16 {
		t.Errorf("RandomHex(8) 应得 16 字符，得到 %d", len(got))
	}
	if got := FirstString(map[string]any{"a": "", "b": " v "}, "a", "b"); got != "v" {
		t.Errorf("FirstString 应跳过空串并裁剪，得到 %q", got)
	}
	if strings.TrimSpace(FirstString(nil, "x")) != "" {
		t.Error("FirstString(nil) 应返回空串")
	}
}
