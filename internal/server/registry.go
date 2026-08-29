package server

import (
	"fmt"
	"sort"
	"sync"
)

// Registry 集中管理上游供应商实例，按 code 分发。
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry 创建空注册表。内置供应商在组装层（httpserver）统一注册。
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register 注册供应商（覆盖写）。
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Code()] = p
}

// ProviderInfo 供应商清单条目（后台下拉等展示用）。
type ProviderInfo struct {
	Code string
	Name string
}

// List 返回已注册供应商清单（按 code 排序，顺序稳定）。
func (r *Registry) List() []ProviderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderInfo, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, ProviderInfo{Code: p.Code(), Name: p.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// CredentialFieldSets 所有供应商的凭据字段集（后台服务器表单动态渲染用）。
func (r *Registry) CredentialFieldSets() map[string][]CredentialField {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string][]CredentialField, len(r.providers))
	for code, p := range r.providers {
		out[code] = CredentialFieldsFor(p)
	}
	return out
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
