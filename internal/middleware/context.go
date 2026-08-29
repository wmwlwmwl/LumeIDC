package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RequireUserOrRedirect 未登录跳转登录页（带回跳地址）。
func RequireUserOrRedirect(w http.ResponseWriter, r *http.Request) (int64, bool) {
	s := FromSession(r.Context())
	if s == nil || s.IsAdmin || s.UserID == 0 {
		http.Redirect(w, r, "/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
		return 0, false
	}
	return s.UserID, true
}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

// FromSession returns the request session; nil if not logged in.
func FromSession(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey).(*Session)
	return s
}

// RequireUser returns the logged-in non-admin user id or writes 401.
// RequireUser 普通用户鉴权：浏览器导航（含表单提交/无该头的旧客户端）跳登录页并带 next；
// AJAX（fetch 发起的请求 Sec-Fetch-Mode 非 navigate）返回 401 JSON，由前端提示“登录已过期”。
func RequireUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	s := FromSession(r.Context())
	if s == nil || s.IsAdmin || s.UserID == 0 {
		if r.Header.Get("Sec-Fetch-Mode") == "navigate" || r.Header.Get("Sec-Fetch-Mode") == "" {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		} else {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"ok":0,"msg":"登录已过期，请重新登录"}`)
		}
		return 0, false
	}
	return s.UserID, true
}

// RequireAdmin returns the admin session or writes 401.
func RequireAdmin(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	s := FromSession(r.Context())
	if s == nil || !s.IsAdmin {
		http.Error(w, "需要管理员登录", http.StatusUnauthorized)
		return nil, false
	}
	return s, true
}
