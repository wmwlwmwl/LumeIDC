package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// panic 必须变成结构化 500：没有恢复层时 net/http 会直接断连，客户端拿不到任何响应。
func TestRecoverTurnsPanicInto500(t *testing.T) {
	h := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应转成 500，实得 %d，响应: %s", w.Code, w.Body.String())
	}
}

// 正常请求不受影响。
func TestRecoverPassesNormalRequest(t *testing.T) {
	h := Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("正常请求不应被改动，实得 %d", w.Code)
	}
}

// ErrAbortHandler 是标准库的正常控制流，必须原样上抛而不是被当成故障。
func TestRecoverRepanicsAbortHandler(t *testing.T) {
	h := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("ErrAbortHandler 应原样上抛，实得 %v", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	t.Fatal("ErrAbortHandler 未被上抛（被错误地当成了 panic）")
}
