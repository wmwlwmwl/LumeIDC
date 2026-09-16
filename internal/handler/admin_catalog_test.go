package handler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

type catalogTestProvider struct {
	server.Provider
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *catalogTestProvider) Catalog(ctx context.Context, _ server.Config) ([]server.UpstreamProduct, error) {
	p.calls.Add(1)
	p.started <- struct{}{}
	select {
	case <-p.release:
		return []server.UpstreamProduct{{PID: 1, Name: "产品"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestProviderCatalogSharesInFlightRequest(t *testing.T) {
	oldCache := catalogCache
	catalogCache = &providerCatalogCache{
		at:       map[int64]time.Time{},
		lists:    map[int64][]server.UpstreamProduct{},
		inflight: map[int64]*providerCatalogCall{},
	}
	t.Cleanup(func() { catalogCache = oldCache })

	p := &catalogTestProvider{started: make(chan struct{}, 2), release: make(chan struct{})}
	sv := &repo.Server{ID: 1}
	leaderDone := make(chan error, 1)
	go func() {
		_, err := (&AdminManage{}).providerCatalog(context.Background(), 1, sv, p, false)
		leaderDone <- err
	}()
	<-p.started

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := (&AdminManage{}).providerCatalog(waitCtx, 1, sv, p, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待者错误 = %v，期望 context deadline exceeded", err)
	}
	otherDone := make(chan error, 1)
	go func() {
		_, err := (&AdminManage{}).providerCatalog(context.Background(), 2, sv, p, false)
		otherDone <- err
	}()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("不同 serverID 被进行中请求阻塞")
	}
	if p.calls.Load() != 2 {
		t.Fatalf("目录调用次数 = %d，期望同 serverID 合并、不同 serverID 并行后的 2 次", p.calls.Load())
	}
	close(p.release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("发起者返回错误: %v", err)
	}
	if err := <-otherDone; err != nil {
		t.Fatalf("不同 serverID 拉取错误: %v", err)
	}
}
