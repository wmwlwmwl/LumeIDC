package service

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/csdk"
	"lumeidc/internal/repo"
)

// 类型别名：适配器实现已迁往 internal/csdk + internal/plugins/captcha，
// 本包保留别名与薄包装，存量业务代码零改动。
type CaptchaProvider = csdk.Provider

type CaptchaPublicConfig = csdk.PublicConfig

type ConfiguredCaptchaProvider struct {
	host *csdk.Host
}

func NewConfiguredCaptchaProvider(settings *repo.Settings) *ConfiguredCaptchaProvider {
	return &ConfiguredCaptchaProvider{
		host: &csdk.Host{Settings: settings, Client: &http.Client{Timeout: 10 * time.Second}},
	}
}

func externalCaptchaSetting(scene string) string {
	switch scene {
	case "register":
		return "external_captcha_register_enabled"
	case "register_code":
		return "external_captcha_register_code_enabled"
	case "forgot_code":
		return "external_captcha_forgot_code_enabled"
	case "profile_code":
		return "external_captcha_profile_code_enabled"
	case "login":
		return "external_captcha_login_enabled"
	case "phone_login_code":
		return "external_captcha_phone_login_code_enabled"
	default:
		return ""
	}
}

// ExternalRequested 表示当前普通场景被配置为使用外部 captcha，即使配置不完整也应 fail closed。
// 场景开关是系统层逻辑，不属于供应商适配器。
func (p *ConfiguredCaptchaProvider) ExternalRequested(ctx context.Context, scene string) bool {
	setting := externalCaptchaSetting(scene)
	return setting != "" && p.host.Get(ctx, setting) == "1"
}

func (p *ConfiguredCaptchaProvider) providerKey(ctx context.Context) string {
	return strings.ToLower(p.host.Get(ctx, "captcha_provider"))
}

// PublicConfig 只对普通用户的外部场景提供安全配置。
// 统一走 csdk 注册表；供应商未注册或配置不完整时返回 Enabled=false。
func (p *ConfiguredCaptchaProvider) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || p.host.Get(ctx, setting) != "1" {
		return out
	}
	adapter, found, err := csdk.New(p.providerKey(ctx), p.host)
	if err != nil || !found {
		log.Printf("[captcha] 验证码供应商未注册或构造失败：%s err=%v", p.providerKey(ctx), err)
		return out
	}
	return adapter.PublicConfig(ctx, scene)
}

// Verify 是服务器端唯一可信验证点。仅在已启用且完整配置的外部场景中执行。
func (p *ConfiguredCaptchaProvider) Verify(ctx context.Context, scene string, payload map[string]string, ip string) error {
	cfg := p.PublicConfig(ctx, scene)
	if !cfg.Enabled {
		return errors.New("外部人机验证未启用或配置不完整")
	}
	adapter, found, err := csdk.New(cfg.Provider, p.host)
	if err != nil || !found {
		return errors.New("验证码供应商适配器未注册：" + cfg.Provider)
	}
	return adapter.Verify(ctx, scene, payload, ip)
}
