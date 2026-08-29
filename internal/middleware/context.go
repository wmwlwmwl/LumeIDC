package middleware

import (
	"context"
	"net/http"
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
func RequireUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	s := FromSession(r.Context())
	if s == nil || s.IsAdmin || s.UserID == 0 {
		http.Error(w, "需要登录", http.StatusUnauthorized)
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
