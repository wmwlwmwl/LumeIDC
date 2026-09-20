package vsdk

import (
	"fmt"
	"strings"
	"sync"
)

// Factory 适配器工厂：由宿主（设置读取 + HTTP 工具）构造 Provider。
type Factory func(h *Host) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]registeredProvider{}
)

type registeredProvider struct {
	descriptor Descriptor
	factory    Factory
}

// Register 注册实名服务商（描述符 + 工厂单入口，适配器包 init 调用）。
// 重复 key 或缺 key panic——注册冲突应启动早期暴露（对齐 plugin.Register 策略）。
func Register(d Descriptor, f Factory) {
	key := strings.ToLower(strings.TrimSpace(d.Key))
	if key == "" {
		panic("vsdk: 服务商缺少 Key")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("vsdk: 服务商重复注册: %s", key))
	}
	registry[key] = registeredProvider{descriptor: d, factory: f}
}

// DescriptorFor 按 key 取描述符。
func DescriptorFor(key string) (Descriptor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[strings.ToLower(strings.TrimSpace(key))]
	return d.descriptor, ok
}

// Registry 返回全部已注册服务商描述符（key → 描述符，拷贝）。
func Registry() map[string]Descriptor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]Descriptor, len(registry))
	for k, v := range registry {
		out[k] = v.descriptor
	}
	return out
}

// New 按 key 创建适配器实例。
func New(key string, h *Host) (Provider, bool, error) {
	registryMu.RLock()
	rp, ok := registry[strings.ToLower(strings.TrimSpace(key))]
	registryMu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	p, err := rp.factory(h)
	if err != nil {
		return nil, true, err
	}
	return p, true, nil
}
