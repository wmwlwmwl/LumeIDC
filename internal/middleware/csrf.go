package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// CSRF protects state-changing requests via double-submit token bound to session.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		s := FromSession(r.Context())
		if s == nil {
			http.Error(w, "会话无效", http.StatusUnauthorized)
			return
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
