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

// DefaultRegistry 进程级默认注册表：各供应商包在 init() 中经 Register 自注册，
// 组合根直接使用。新增供应商 = 新目录 + 包内一行 init，无需改动组合根。
// 测试用 NewRegistry 建独立实例，与默认表隔离。
var DefaultRegistry = NewRegistry()

// Register 向 DefaultRegistry 注册供应商（覆盖写）。
func Register(p Provider) { DefaultRegistry.Register(p) }

// NewRegistry 创建空注册表（测试隔离用；生产路径用 DefaultRegistry）。
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

// ProductFormHintsSets 所有供应商的产品表单差异声明；MarkupFree 从能力接口自动合并。
func (r *Registry) ProductFormHintsSets() map[string]ProductFormHints {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]ProductFormHints, len(r.providers))
	for code, p := range r.providers {
		var h ProductFormHints
		if fp, ok := p.(ProductFormProvider); ok {
			h = fp.ProductFormHints()
		}
		if mf, ok := p.(MarkupFreeProvider); ok && mf.MarkupFree() {
			h.MarkupFree = true
		}
		out[code] = h
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
		return nil, fmt.Errorf("不支持的供应商: %s", code)
	}
	return p, nil
}
