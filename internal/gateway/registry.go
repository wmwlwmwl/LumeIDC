package gateway

import "sync"

// 网关驱动注册表：各驱动包内 init() 自注册（与 service 包短信/验证码适配器同模式）。
// 新增网关 = 新文件实现 Gateway + 一行 init，组合根与定时任务均无需改动。

var (
	regMu sync.RWMutex
	reg   = map[string]Gateway{}
)

// Register 注册支付网关驱动，以 Driver() 为键（重复注册覆盖）。
func Register(g Gateway) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[g.Driver()] = g
}

// All 返回全部已注册网关（driver → 实例，map 为拷贝）。
func All() map[string]Gateway {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make(map[string]Gateway, len(reg))
	for k, v := range reg {
		out[k] = v
	}
	return out
}

// OrderQueriers 返回实现了 OrderQuerier 的网关（异步通知丢失时补单用）。
func OrderQueriers() map[string]OrderQuerier {
	regMu.RLock()
	defer regMu.RUnlock()
	out := map[string]OrderQuerier{}
	for driver, impl := range reg {
		if q, ok := impl.(OrderQuerier); ok {
			out[driver] = q
		}
	}
	return out
}
