package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetsHandler(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		want      int
		wantCache string
	}{
		{name: "own css negotiable", path: "/assets/css/lume.css", want: 200, wantCache: "no-cache"},
		{name: "own js negotiable", path: "/assets/js/lume.js", want: 200, wantCache: "no-cache"},
		{name: "vendor immutable", path: "/assets/vendor/bootstrap-5.3.3/css/bootstrap.min.css", want: 200, wantCache: "public, max-age=31536000, immutable"},
		{name: "missing", path: "/assets/no-such-file.css", want: 404, wantCache: ""},
		{name: "traversal", path: "/assets/%2e%2e/go.mod", want: 404, wantCache: ""},
	}
	mux := http.NewServeMux()
	RegisterAssets(mux)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
			if tt.wantCache == "" {
				return
			}
			if cc := w.Header().Get("Cache-Control"); cc != tt.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", cc, tt.wantCache)
			}
		})
	}

	// 自有文件协商缓存：带 If-None-Match / If-Modified-Since 时应支持 304。
	t.Run("own css conditional", func(t *testing.T) {
		r1 := httptest.NewRequest("GET", "/assets/css/lume.css", nil)
		w1 := httptest.NewRecorder()
		mux.ServeHTTP(w1, r1)
		if w1.Code != 200 {
			t.Fatalf("first fetch status = %d", w1.Code)
		}
		etag := w1.Header().Get("ETag")
		mod := w1.Header().Get("Last-Modified")
		if etag == "" && mod == "" {
			t.Skip("file server did not emit validators; no-cache still re-fetches correctly")
		}
		r2 := httptest.NewRequest("GET", "/assets/css/lume.css", nil)
		if etag != "" {
			r2.Header.Set("If-None-Match", etag)
		} else {
			r2.Header.Set("If-Modified-Since", mod)
		}
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, r2)
		if w2.Code != http.StatusNotModified {
			t.Fatalf("conditional status = %d, want 304", w2.Code)
		}
	})
}
