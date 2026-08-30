package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetsHandler(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "embedded css", path: "/assets/css/lume.css", want: 200},
		{name: "missing", path: "/assets/no-such-file.css", want: 404},
		{name: "traversal", path: "/assets/%2e%2e/go.mod", want: 404},
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
			if tt.want == 200 {
				if !strings.Contains(w.Header().Get("Content-Type"), "text/css") {
					t.Fatalf("Content-Type = %q, want CSS", w.Header().Get("Content-Type"))
				}
				if w.Header().Get("Cache-Control") == "" {
					t.Fatal("missing Cache-Control")
				}
			}
		})
	}
}
