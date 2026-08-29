package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRF(t *testing.T) {
	var called int
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
		{name: "缺少令牌", wantStatus: http.StatusForbidden},
		{name: "错误令牌", token: "wrong", wantStatus: http.StatusForbidden},
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
		})
	}
}
