package service

import (
	"net/http"
	"strings"

	"lumeidc/internal/repo"
)

// captchaAdapterFactory 根据 provider key 和 settings 创建 CaptchaProvider 的工厂函数。
// 每个供应商适配器在自己的文件中通过 init() 注册。
// 新增供应商只需新增文件并注册工厂，无需改动业务层和已有适配器。
type captchaAdapterFactory func(settings *repo.Settings, client *http.Client) CaptchaProvider

var captchaAdapterFactories = map[string]captchaAdapterFactory{}

// registerCaptchaAdapter 注册一个验证码供应商工厂。
func registerCaptchaAdapter(key string, factory captchaAdapterFactory) {
	key = strings.ToLower(strings.TrimSpace(key))
	captchaAdapterFactories[key] = factory
}

// captchaFromRegistry 从注册表按 key 创建 CaptchaProvider。
// 返回 (provider, found)。found=false 表示该 key 未注册到新适配器。
func captchaFromRegistry(key string, settings *repo.Settings, client *http.Client) (CaptchaProvider, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	factory, ok := captchaAdapterFactories[key]
	if !ok {
		return nil, false
	}
	return factory(settings, client), true
}
