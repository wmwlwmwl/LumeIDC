package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

// CSRF protects state-changing requests via double-submit token bound to session.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// 支付平台服务器通知没有浏览器 session；notify 自身必须做网关签名、金额、幂等校验。
		if r.URL.Path == "/pay/notify" {
			next.ServeHTTP(w, r)
			return
		}
		s := FromSession(r.Context())
		if s == nil {
			http.Error(w, "会话无效", http.StatusUnauthorized)
			return
		}
		// 在读取 multipart 表单前限制请求体，避免 CSRF 校验触发解析时接收超大上传。
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/") {
			r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		}
		tok := r.Header.Get("X-CSRF-Token")
		if tok == "" {
			tok = r.PostFormValue("_csrf")
		}
		if tok == "" || !hmac.Equal([]byte(tok), []byte(s.CSRFToken())) {
			http.Error(w, "CSRF 校验失败", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewCSRFToken generates a per-session random token.
func NewCSRFToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure must not produce a predictable token; callers cannot
		// safely continue without a valid per-session CSRF secret.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
