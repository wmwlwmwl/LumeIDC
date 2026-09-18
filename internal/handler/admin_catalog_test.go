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

func TestCatalogTypeOptionsEasyPanelUsesExistingChildren(t *testing.T) {
	types := []repo.ProductType{
		{ID: 1, Name: "云主机", ParentID: 0},
		{ID: 2, Name: "国内线路", ParentID: 1},
		{ID: 3, Name: "隐藏二级", ParentID: 1, Hidden: true},
		{ID: 4, Name: "其他", ParentID: 0},
	}
	got := catalogTypeOptions(types, "easypanel")
	if len(got) != 1 || got[0].ID != 2 || got[0].ParentID != 1 || got[0].Name != "云主机 / 国内线路" {
		t.Fatalf("EasyPanel 分类选项=%+v，期望只返回已有二级分类", got)
	}
	other := catalogTypeOptions(types, "zjmf")
	if len(other) != 2 || other[0].ParentID != 0 || other[1].ParentID != 0 {
		t.Fatalf("其他供应商分类选项=%+v，期望返回一级分类", other)
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
