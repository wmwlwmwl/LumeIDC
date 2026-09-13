package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRF(t *testing.T) {	var called int
	h := CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	sess := &Session{CSRF: NewCSRFToken()}

	form := url.Values{"_csrf": {sess.CSRFToken()}}
	cases := []struct {
		name       string
		token      string
		wantStatus int
		wantCalled bool
	}{
		{name: "缺少令牌", wantStatus: http.StatusSeeOther},
		{name: "错误令牌", token: "wrong", wantStatus: http.StatusSeeOther},
		{name: "正确令牌", token: form.Get("_csrf"), wantStatus: http.StatusNoContent, wantCalled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := called
			body := url.Values{}
			if tc.token != "" {
				body.Set("_csrf", tc.token)
			}
			req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(body.Encode()))
			req = req.WithContext(WithSession(req.Context(), sess))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("状态码=%d，期望=%d", w.Code, tc.wantStatus)
			}
			if (called > before) != tc.wantCalled {
				t.Fatalf("处理器调用状态错误，called=%d before=%d", called, before)
			}
			if tc.wantStatus == http.StatusSeeOther && !strings.HasPrefix(w.Header().Get("Location"), "/login") {
				t.Fatalf("应跳转登录页，Location=%q", w.Header().Get("Location"))
			}
		})
	}
}

// TestCSRFExpiredSessionAJAXJSON 覆盖前端会话恢复依赖的契约：
// AJAX 请求在会话/CSRF 失效时返回 401 JSON（而非登录页 HTML），且消息可被前端识别。
func TestCSRFExpiredSessionAJAXJSON(t *testing.T) {
	h := CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("无会话返回会话已过期", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(""))
		req.Header.Set("Sec-Fetch-Mode", "cors")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("状态码=%d，期望 401", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("AJAX 应返回 JSON，Content-Type=%q", ct)
		}
		if body := w.Body.String(); !strings.Contains(body, "会话已过期") {
			t.Fatalf("响应体应包含“会话已过期”，实际=%q", body)
		}
	})

	t.Run("CSRF 不匹配返回页面已过期", func(t *testing.T) {
		sess := &Session{CSRF: NewCSRFToken()}
		req := httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(""))
		req = req.WithContext(WithSession(req.Context(), sess))
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("X-CSRF-Token", "stale-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("状态码=%d，期望 401", w.Code)
		}
		if body := w.Body.String(); !strings.Contains(body, "页面已过期") {
			t.Fatalf("响应体应包含“页面已过期”，实际=%q", body)
		}
	})

	t.Run("导航请求失效时跳转登录页", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/product", strings.NewReader(""))
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("状态码=%d，期望 303", w.Code)
		}
		if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/admin/login") {
			t.Fatalf("后台请求应跳后台登录，Location=%q", loc)
		}
	})
}
