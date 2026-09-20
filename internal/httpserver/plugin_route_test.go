package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 插件子树挂载与 SPA 兜底 "GET /{path...}" 不得冲突：
// 无方法的全路前缀模式会与兜底构成"路径宽方法窄 vs 路径窄方法宽"重叠导致 ServeMux panic。
// 这里按 server.go 的同款挂载方式回归（Build 需要数据库，无法直接构造）。
func TestPluginSubtreeCoexistsWithSPAFallback(t *testing.T) {
	mux := http.NewServeMux()
	// webui.go 的 SPA 兜底（仅 GET）
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(299) // 标记命中兜底
	})
	// 插件子树：带方法的 GET/POST 前缀（与 Build 中的挂载循环一致）
	sub := http.NewServeMux()
	sub.HandleFunc("GET /list", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(298) // 标记命中插件
	})
	gated := http.StripPrefix("/plugin/demo", sub)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		mux.Handle(method+" /plugin/demo/", gated)
	}

	// 注册过程不 panic（ServeMux 在注册时检测冲突）
	// GET 插件 API 命中插件，POST 命中插件，GET 其他路径落兜底
	if code := status(mux, "GET", "/plugin/demo/list"); code != 298 {
		t.Fatalf("插件 GET 应命中子树, got %d", code)
	}
	if code := status(mux, "POST", "/plugin/demo/list"); code != 405 {
		t.Fatalf("插件 POST 应被子树接管（子路由无 POST → 405）, got %d", code)
	}
	if code := status(mux, "GET", "/anything/else"); code != 299 {
		t.Fatalf("其他 GET 应落 SPA 兜底, got %d", code)
	}
}

func status(mux *http.ServeMux, method, path string) int {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code
}
