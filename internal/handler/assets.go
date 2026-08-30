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
func AssetsHandler() http.Handler {
	root, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(root))
	return http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All UI assets are versioned or immutable application files.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
}

func RegisterAssets(mux *http.ServeMux) {
	mux.Handle("GET /assets/", AssetsHandler())
}
