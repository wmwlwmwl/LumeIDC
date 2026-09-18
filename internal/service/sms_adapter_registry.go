package service

import (
	"net/http"
	"strings"
)

// smsProviderFactory 根据 key 创建 SMSProvider 实例的工厂函数。
// 每个供应商适配器在自己的文件中通过 init() 注册。
// 新增供应商只需新增文件并注册工厂，无需改动业务层和已有适配器。
type smsProviderFactory func(settings map[string]string, client *http.Client) (SMSProvider, error)

var smsProviderFactories = map[string]smsProviderFactory{}

// registerSMSProvider 注册一个短信供应商工厂。
func registerSMSProvider(key string, factory smsProviderFactory) {
	key = strings.ToLower(strings.TrimSpace(key))
	smsProviderFactories[key] = factory
}

// smsProviderFromRegistry 从注册表按 key 创建 SMSProvider。
// 返回 (provider, found, error)。found=false 表示该 key 未注册到新适配器。
func smsProviderFromRegistry(key string, settings map[string]string, client *http.Client) (SMSProvider, bool, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	factory, ok := smsProviderFactories[key]
	if !ok {
		return nil, false, nil
	}
	provider, err := factory(settings, client)
	if err != nil {
		return nil, true, err
	}
	return provider, true, nil
}
