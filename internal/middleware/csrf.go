package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
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
			// 会话缺失/失效（如服务重启导致内存会话丢失）：友好跳转登录页而非裸报错。
			RedirectToLogin(w, r, "会话已过期，请重新登录")
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
			// 令牌与当前会话不匹配（旧页面/会话轮换）：跳转登录页刷新会话与令牌。
			RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RedirectToLogin 会话/CSRF 失效时友好跳转登录页（供中间件与各 handler 复用）。
// admin 请求跳后台登录（Location 会被自定义后台路径中间件改写到真实路径）；其余跳前台登录。
func RedirectToLogin(w http.ResponseWriter, r *http.Request, msg string) {
	target := "/login"
	if strings.HasPrefix(r.URL.Path, "/admin") {
		target = "/admin/login"
	}
	http.Redirect(w, r, target+"?err="+url.QueryEscape(msg), http.StatusSeeOther)
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
