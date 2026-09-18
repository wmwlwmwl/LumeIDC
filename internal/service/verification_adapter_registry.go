package service

import (
	"strings"
)

// verificationAdapterFactory 根据 key 创建 VerificationProvider 实例的工厂函数。
// 每个供应商适配器在自己的文件中通过 init() 注册。
// 新增供应商只需新增文件并注册工厂，无需改动业务层和已有适配器。
type verificationAdapterFactory func(factory *ConfiguredVerificationProvider) (VerificationProvider, error)

var verificationAdapterRegistry = map[string]verificationAdapterFactory{}

// registerVerificationAdapter 注册一个实名供应商工厂。
func registerVerificationAdapter(key string, factory verificationAdapterFactory) {
	key = strings.ToLower(strings.TrimSpace(key))
	verificationAdapterRegistry[key] = factory
}

// verificationFromRegistry 从注册表按 key 创建 VerificationProvider。
// 返回 (provider, found, error)。found=false 表示该 key 未注册到新适配器。
func verificationFromRegistry(key string, factory *ConfiguredVerificationProvider) (VerificationProvider, bool, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	f, ok := verificationAdapterRegistry[key]
	if !ok {
		return nil, false, nil
	}
	provider, err := f(factory)
	if err != nil {
		return nil, true, err
	}
	return provider, true, nil
}
