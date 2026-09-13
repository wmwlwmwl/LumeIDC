package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ctxActAsUserKey 管理员代管：让请求以「目标服务的归属用户」身份执行用户侧逻辑。
// 只有后台代管包装器（/admin/services/{id}/... 校验管理员会话后）才会写入，
// 普通请求上下文里不存在该值，因此不会放宽前台鉴权。
type ctxActAsUserKey struct{}

// WithActAsUser 标记当前请求以 userID 的身份执行用户侧 handler。
func WithActAsUser(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxActAsUserKey{}, userID)
}

// actAsUser 读取代管身份。
func actAsUser(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxActAsUserKey{}).(int64)
	return id, ok && id > 0
}

// RequireUserOrRedirect 未登录跳转登录页（带回跳地址）。
func RequireUserOrRedirect(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if uid, ok := actAsUser(r.Context()); ok {
		return uid, true
	}
	s := FromSession(r.Context())
	if s == nil || s.IsAdmin || s.UserID == 0 {
		http.Redirect(w, r, "/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
		return 0, false
	}
	return s.UserID, true
}

// ctxSessions 请求上下文里挂载的两个登录通道会话：
// current 为当前请求通道（后台路径取管理员通道，其余取用户通道），
// other 为另一通道（后台登录前管理员通道尚未建立时，用它校验页面会话的 CSRF）。
type ctxSessions struct {
	current *Session
	other   *Session
}

// WithSession 设置当前通道会话；上下文已由 Store.Middleware 初始化时保留另一通道会话
// （handler 启动匿名会话后不能再把管理员通道挤掉）。
func WithSession(ctx context.Context, s *Session) context.Context {
	if cur, ok := ctx.Value(sessionKey).(*ctxSessions); ok {
		cp := *cur
		cp.current = s
		return context.WithValue(ctx, sessionKey, &cp)
	}
	return context.WithValue(ctx, sessionKey, &ctxSessions{current: s})
}

func withOtherSession(ctx context.Context, s *Session) context.Context {
	if cur, ok := ctx.Value(sessionKey).(*ctxSessions); ok {
		cp := *cur
		cp.other = s
		return context.WithValue(ctx, sessionKey, &cp)
	}
	return context.WithValue(ctx, sessionKey, &ctxSessions{other: s})
}

// FromSession returns the request session; nil if not logged in.
func FromSession(ctx context.Context) *Session {
	if cs, ok := ctx.Value(sessionKey).(*ctxSessions); ok {
		return cs.current
	}
	return nil
}

// fromOtherSession 返回另一通道会话（同包内 CSRF 校验用）。
func fromOtherSession(ctx context.Context) *Session {
	if cs, ok := ctx.Value(sessionKey).(*ctxSessions); ok {
		return cs.other
	}
	return nil
}

// RequireUser returns the logged-in non-admin user id or writes 401.
// RequireUser 普通用户鉴权：浏览器导航（含表单提交/无该头的旧客户端）跳登录页并带 next；
// AJAX（fetch 发起的请求 Sec-Fetch-Mode 非 navigate）返回 401 JSON，由前端提示“登录已过期”。
func RequireUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	// 管理员代管（包装器已校验管理员会话）：直接以服务归属用户身份放行。
	if uid, ok := actAsUser(r.Context()); ok {
		return uid, true
	}
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
