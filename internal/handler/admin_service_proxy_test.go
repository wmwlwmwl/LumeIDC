package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lumeidc/internal/middleware"
)

// TestAdminServiceProxyRegistrationNoConflict 后台代管路由必须能与既有的
// /admin/services、/admin/services/{id}/action、/admin/services/status 共存
// （ServeMux 对重叠的具名模式会 panic）。
func TestAdminServiceProxyRegistrationNoConflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/services", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /admin/services/status", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /admin/services/{id}/action", func(http.ResponseWriter, *http.Request) {})

	h := &Pages{DB: nil}
	h.RegisterAdminServiceProxy(mux) // 冲突会在此 panic
}

// TestAdminServiceProxyRequiresAdmin 未登录后台时不得进入代管逻辑：
// 应直接 401，而不是去查服务归属用户（Pages.DB 为 nil，一旦穿透就会 panic）。
func TestAdminServiceProxyRequiresAdmin(t *testing.T) {
	mux := http.NewServeMux()
	h := &Pages{}
	h.RegisterAdminServiceProxy(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/services/1", nil)
	req.Header.Set("Accept", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问代管接口应 401，实得 %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestRequireUserHonoursActAsUser 代管上下文应让用户侧 handler 以归属用户身份放行，
// 且不影响无代管的正常鉴权。
func TestRequireUserHonoursActAsUser(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/services/1", nil)
	req.Header.Set("Accept", "application/json")

	// 无会话无代管 → 401
	if _, ok := middleware.RequireUser(rec, req); ok {
		t.Fatal("无会话时不应放行")
	}

	// 注入代管后 → 放行并返回归属用户
	ctx := middleware.WithActAsUser(req.Context(), 42)
	rec2 := httptest.NewRecorder()
	uid, ok := middleware.RequireUser(rec2, req.WithContext(ctx))
	if !ok || uid != 42 {
		t.Fatalf("代管上下文应放行并返回 root 用户，实得 uid=%d ok=%v", uid, ok)
	}
}
