package handler

import (
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumeidc/internal/config"
	"lumeidc/internal/middleware"
)

func testStore(t *testing.T) *middleware.Store {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	s, err := middleware.NewStore(&config.Config{SecretKey: key})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// TestWebUIRegistrationNoConflict 确保 SPA 兜底路由与现有 SSR 路由共存不 panic
// （ServeMux 会拒绝相互重叠的具名模式；不同方法可重叠）。
func TestWebUIRegistrationNoConflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("SSR-products")) })
	mux.HandleFunc("POST /products", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(405) })
	RegisterWebUI(mux) // 若模式冲突会在此 panic
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	_ = mux
}

// TestWebUISpecificRouteWins 具体 SSR 路由优先于 SPA 兜底。
func TestWebUISpecificRouteWins(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("SSR-products")) })
	RegisterWebUI(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/products", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "SSR-products" {
		t.Fatalf("specific SSR route must win, got code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestWebUISPAFallbackKnownRoute 未匹配的 GET 落到 SPA 文档（存在 dist 构建时）。
func TestWebUISPAFallbackKnownRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("SSR-products")) })
	RegisterWebUI(mux)

	// dist 构建产物存在：兜底应返回 index.html 文档。
	if _, err := fs.Stat(webUIFS, "webui/dist/index.html"); err != nil {
		t.Skip("webui/dist 未构建，跳过 SPA 兜底断言")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/some/unknown", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="app">`) {
		t.Fatalf("SPA fallback expected index.html, got code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestSpaOwned SPA 接管路径白名单：已迁移路径接管，SSR 专属路径必须保留。
func TestSpaOwned(t *testing.T) {
	owned := []string{"/", "/cart", "/services", "/notifications", "/user",
		"/user/recharge", "/user/invoices", "/user/password", "/user/profile",
		"/login", "/register", "/user/verification",
		"/buy/20", "/pay/12", "/services/12", "/services/12/upgrade", "/services/12/console"}
	for _, p := range owned {
		if !spaOwned(p) {
			t.Errorf("spaOwned(%q) = false, want true", p)
		}
	}
	// 必须保留 SSR 的路径（服务子路径/回调）
	notOwned := []string{
		"/services/1/module/nat", "/services/1/chart", "/services/1/vnc-ws",
		"/pay/notify", "/pay/qr", "/pay/12/status",
		"/install", "/admin/users", "/lumeidc/login", "/services/abc"}
	for _, p := range notOwned {
		if spaOwned(p) {
			t.Errorf("spaOwned(%q) = true, want false（该路径必须由 SSR 承载）", p)
		}
	}
}

// TestSPAGate 导航命中接管路径时返回 SPA 外壳；API 请求与未迁移路径透传。
func TestSPAGate(t *testing.T) {
	if _, err := fs.Stat(webUIFS, "webui/dist/index.html"); err != nil {
		t.Skip("webui/dist 未构建，SPAGate 不生效")
	}
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte("SSR"))
	})
	h := SPAGate(next)

	// 1) 浏览器导航 → SPA 外壳
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/cart", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	h.ServeHTTP(rec, r)
	if reached || !strings.Contains(rec.Body.String(), `<div id="app">`) {
		t.Fatalf("导航 /cart 应返回 SPA 外壳，reached=%v body=%q", reached, rec.Body.String())
	}

	// 2) API 请求（Accept: json）→ 透传
	reached = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/cart", nil)
	r.Header.Set("Accept", "application/json")
	h.ServeHTTP(rec, r)
	if !reached {
		t.Fatal("API 请求应透传给下游（JSON 处理）")
	}

	// 3) 未接管路径（SSR 专属）→ 透传
	reached = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/services/1/chart", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(rec, r)
	if !reached {
		t.Fatal("未接管路径应透传给 SSR")
	}
}

// TestSPAGateAdminEntry 后台 SPA 入口：/admin 与 /admin/login 导航返回 admin.html；
// 后台其它子路径（SSR 表单/API）与 JSON 请求透传。
func TestSPAGateAdminEntry(t *testing.T) {
	if _, err := fs.Stat(webUIFS, "webui/dist/admin.html"); err != nil {
		t.Skip("webui/dist 未构建，SPAGate 不生效")
	}
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte("SSR"))
	})
	h := SPAGate(next)

	// 1) /admin 导航 → 后台 SPA 外壳
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(rec, r)
	if reached || !strings.Contains(rec.Body.String(), "admin-app") {
		t.Fatalf("/admin 应返回 admin.html，reached=%v body=%q", reached, rec.Body.String())
	}

	// 2) /admin/login 导航 → 后台 SPA 外壳（Art 登录页，含图形验证码/TOTP）
	reached = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/login", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(rec, r)
	if reached || !strings.Contains(rec.Body.String(), "admin-app") {
		t.Fatalf("/admin/login 应返回 admin.html，reached=%v body=%q", reached, rec.Body.String())
	}

	// 3) /admin/products（SSR 表单）→ 透传
	reached = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/products", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(rec, r)
	if !reached {
		t.Fatal("/admin/products 应透传给 SSR")
	}

	// 4) /admin 的 JSON 请求（看板数据）→ 透传
	reached = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin", nil)
	r.Header.Set("Accept", "application/json")
	h.ServeHTTP(rec, r)
	if !reached {
		t.Fatal("/admin 的 JSON 请求应透传（看板 API）")
	}
}

func TestSessionBootstrapThroughMiddleware(t *testing.T) {
	store := testStore(t)
	deps := &Deps{PageStore: store, AdminStore: store}
	h := &Session{Deps: deps}
	mux := http.NewServeMux()
	h.Register(mux)
	handler := store.Middleware(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/session", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("session code=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"csrf"`, `"site"`, `"admin":{"path":"/admin","user":null}`, `"user":null`} {
		if !strings.Contains(body, want) {
			t.Fatalf("session bootstrap missing %s in %q", want, body)
		}
	}
	// 应下发会话 cookie，供后续写请求复用 CSRF 令牌
	var hasCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lume_session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("expected lume_session cookie to be set")
	}
}

// TestAdminPathJSONPassthrough 自定义后台路径改写器应跳过 application/json 响应，
// 避免改写 JSON body 中的 /admin 子串（SPA 时代新增端点依赖此行为）。
func TestAdminPathJSONPassthrough(t *testing.T) {
	store := testStore(t)
	deps := &Deps{PageStore: store, AdminStore: store, AdminPathCfg: middleware.NewAdminPathConfig("panel")}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(`{"msg":"keep /admin as-is"}`))
	})
	handler := middleware.AdminPath(inner, deps.AdminPathCfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/panel/test", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "/admin") {
		t.Fatalf("JSON body must NOT be rewritten under custom admin path, got %q", got)
	}
}
