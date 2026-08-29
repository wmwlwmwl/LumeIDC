package server

import (
	"fmt"
	"sync"
)

// Registry 集中管理上游供应商实例，按 code 分发。
// 默认注册 zjmf；后续可扩展其他供应商。
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry 创建并注册内置供应商。
func NewRegistry() *Registry {
	r := &Registry{providers: map[string]Provider{}}
	return r
}

// Register 注册供应商（覆盖写）。
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Code()] = p
}

// Get 按 code 获取供应商；空字符串默认 "zjmf"；未知 code 返回错误。
func (r *Registry) Get(code string) (Provider, error) {
	if code == "" {
		code = "zjmf"
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[code]
	if !ok {
		return nil, fmt.Errorf("不支持的上游供应商: %s", code)
	}
	return p, nil
}

// Default 返回默认供应商（zjmf）。
func (r *Registry) Default() Provider {
	p, _ := r.Get("zjmf")
	return p
}
