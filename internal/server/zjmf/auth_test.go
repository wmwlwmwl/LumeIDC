package zjmf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lumeidc/internal/server"
)

func TestEnsureTokenSharesInFlightLogin(t *testing.T) {
	oldCache := cache
	cache = &authCache{tokens: map[string]jwtEntry{}, inflight: map[string]*loginCall{}}
	t.Cleanup(func() { cache = oldCache })

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zjmf_api_login" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		logins++
		mu.Unlock()
		close(started)
		<-release
		loginOK(w)
	}))
	defer srv.Close()
	cfg := server.Config{APIURL: srv.URL, APIUsername: "api", APIKey: "secret", CredentialRevision: 1}

	leaderDone := make(chan error, 1)
	go func() {
		_, err := ensureToken(context.Background(), cfg)
		leaderDone <- err
	}()
	<-started

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ensureToken(waitCtx, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待者错误 = %v，期望 context deadline exceeded", err)
	}
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("发起者返回错误: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if logins != 1 {
		t.Fatalf("登录请求次数 = %d，期望 1", logins)
	}
}
