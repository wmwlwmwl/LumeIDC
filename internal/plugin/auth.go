package plugin

import (
	"encoding/json"
	"net/http"

	"lumeidc/internal/middleware"
)

// AdminOK 管理员会话校验（与 handler.adminRequire 同判据）；
// 未通过时写 401 JSON 并返回 false，插件 handler 直接 return。
func AdminOK(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess != nil && sess.IsAdmin {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"ok":0,"msg":"登录已过期，请重新登录"}`))
	return false
}

// RequireUserID 用户登录校验；通过返回用户 ID，否则写 401 JSON 并返回 false。
func RequireUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	sess := middleware.FromSession(r.Context())
	if sess != nil && sess.UserID > 0 {
		return sess.UserID, true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"ok":0,"msg":"请先登录"}`))
	return 0, false
}

// WriteJSON 插件 handler 的 JSON 输出（与 handler.writeJSON 同约定：{ok:1,...} 成功、
// {ok:0,msg} 失败，前端 http 封装以 ok 判定）。
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// JSONFail 输出 {ok:0,msg} 失败响应（HTTP 200，前端以 ok 判定）。
func JSONFail(w http.ResponseWriter, msg string) {
	WriteJSON(w, map[string]any{"ok": 0, "msg": msg})
}

// StatusFail 输出带 HTTP 状态码的失败响应（{ok:0,msg}；前端 http 封装对非 2xx 抛错，
// 与核心 jsonStatus 的 SPA 分支等价）。参数校验/不存在/服务器错误用它。
func StatusFail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	WriteJSON(w, map[string]any{"ok": 0, "msg": msg})
}

// AdminSession 取管理员会话（未登录/非管理员写 401 并返回 nil,false）。
// 需要会话上的管理员 ID（如操作审计）时使用；只需门禁用 AdminOK。
func AdminSession(w http.ResponseWriter, r *http.Request) (*middleware.Session, bool) {
	sess := middleware.FromSession(r.Context())
	if sess != nil && sess.IsAdmin {
		return sess, true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"ok":0,"msg":"登录已过期，请重新登录"}`))
	return nil, false
}
