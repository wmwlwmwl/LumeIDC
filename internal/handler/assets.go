package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets
var assetsFS embed.FS

// AssetsHandler serves only the files embedded in the application binary.
// vendor/ 目录内容随二进制固定不变，可长缓存；
// 自有 css/js 会随版本更新，用协商缓存保证变更后立即生效。
func AssetsHandler() http.Handler {
	root, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(root))
	return http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}))
}

func RegisterAssets(mux *http.ServeMux) {
	mux.Handle("GET /assets/", AssetsHandler())
}
