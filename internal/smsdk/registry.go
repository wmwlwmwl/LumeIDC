package smsdk

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Factory 适配器工厂：由服务商配置与 HTTP 客户端构造 Provider。
// client 可为 nil（DoRequest 会兜底默认客户端）。
type Factory func(settings map[string]string, client *http.Client) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]registeredProvider{}
)

type registeredProvider struct {
	descriptor ProviderDescriptor
	factory    Factory
}

// RegisterSMSProvider 注册服务商（描述符 + 工厂单入口，适配器包 init 调用）。
// 重复 key 或缺 key panic——注册冲突应启动早期暴露（对齐 plugin.Register 策略）。
func RegisterSMSProvider(d ProviderDescriptor, f Factory) {
	key := strings.ToLower(strings.TrimSpace(d.Key))
	if key == "" {
		panic("smsdk: 服务商缺少 Key")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("smsdk: 服务商重复注册: %s", key))
	}
	registry[key] = registeredProvider{descriptor: d, factory: f}
}

// DescriptorFor 按 key 取描述符。
func DescriptorFor(key string) (ProviderDescriptor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[strings.ToLower(strings.TrimSpace(key))]
	return d.descriptor, ok
}

// Registry 返回全部已注册服务商描述符（key → 描述符，拷贝）。
func Registry() map[string]ProviderDescriptor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]ProviderDescriptor, len(registry))
	for k, v := range registry {
		out[k] = v.descriptor
	}
	return out
}

// Supports 能力判定：营销范围禁发 OTP；按范围包含 + 类型能力位判定。
func Supports(key string, r Range, kind string) bool {
	if r == "" {
		r = RangeCN
	}
	d, ok := DescriptorFor(key)
	if !ok || (r == RangeMarketing && kind == "otp") {
		return false
	}
	for _, x := range d.Capabilities.Ranges {
		if x == r {
			return kind == "otp" && d.Capabilities.OTP || kind == "notification" && d.Capabilities.Notification
		}
	}
	return false
}

// NewProvider 按 key 创建适配器实例。
func NewProvider(key string, settings map[string]string, client *http.Client) (Provider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if _, ok := DescriptorFor(key); !ok {
		return nil, fmt.Errorf("短信服务商不受支持")
	}
	registryMu.RLock()
	rp, ok := registry[key]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("短信服务商适配器未注册：%s", key)
	}
	return rp.factory(settings, client)
}
