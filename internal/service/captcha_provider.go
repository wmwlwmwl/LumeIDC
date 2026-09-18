package service

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

const maxCaptchaResponseBytes = 64 << 10

// CaptchaPublicConfig 是唯一允许暴露给浏览器的外部验证码信息。
type CaptchaPublicConfig struct {
	Enabled    bool   `json:"enabled"`
	Provider   string `json:"provider,omitempty"`
	PublicID   string `json:"public_id,omitempty"`
	SDKURL     string `json:"sdk_url,omitempty"`
	ScriptURL  string `json:"script_url,omitempty"`
	SDKVersion string `json:"sdk_version,omitempty"`
	RenderMode string `json:"render_mode,omitempty"`
	APIBaseURL string `json:"api_base_url,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Scene      string `json:"scene"`
}

// CaptchaProvider 为普通注册/登录场景提供外部人机验证；本地强制验证码不经过此接口。
type CaptchaProvider interface {
	ExternalRequested(context.Context, string) bool
	PublicConfig(context.Context, string) CaptchaPublicConfig
	Verify(context.Context, string, map[string]string, string) error
}

type ConfiguredCaptchaProvider struct {
	Settings *repo.Settings
	Client   *http.Client
}

func NewConfiguredCaptchaProvider(settings *repo.Settings) *ConfiguredCaptchaProvider {
	return &ConfiguredCaptchaProvider{Settings: settings, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (p *ConfiguredCaptchaProvider) get(ctx context.Context, key string) string {
	if p == nil || p.Settings == nil {
		return ""
	}
	v, _ := p.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
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

// captchaSDK 返回 provider 对应的前端 SDK URL 和版本。
// 被适配器文件和测试共用的纯工具函数。
func captchaSDK(provider string) (url, version string) {
	switch provider {
	case "geetest":
		return "https://static.geetest.com/v4/gt4.js", "v4"
	case "vaptcha":
		return "https://cdn4.vaptcha.com/src/v4.js", "v4"
	case "corptcha":
		return "https://res.25y.cn/corptcha/corptcha.iife.js", "v1"
	default:
		return "", ""
	}
}

// captchaPublicProviderConfig 构造 provider 的公开安全配置基础骨架。
// 被适配器文件和测试共用的纯工具函数。
func captchaPublicProviderConfig(provider string, publicID string, scene string) CaptchaPublicConfig {
	sdk, version := captchaSDK(provider)
	cfg := CaptchaPublicConfig{Scene: scene, Provider: provider, PublicID: publicID, SDKURL: sdk, ScriptURL: sdk, SDKVersion: version}
	if provider == "corptcha" {
		cfg.RenderMode = "inline"
		cfg.APIBaseURL = "https://cpt-api.25y.cn"
		cfg.Purpose = scene
	}
	return cfg
}

// ExternalRequested 表示当前普通场景被配置为使用外部 captcha，即使配置不完整也应 fail closed。
func (p *ConfiguredCaptchaProvider) ExternalRequested(ctx context.Context, scene string) bool {
	return externalCaptchaSetting(scene) != "" && p.get(ctx, externalCaptchaSetting(scene)) == "1"
}

// PublicConfig 只对普通用户的三个外部场景提供安全配置。
// 统一走适配器注册表，不再有旧版 switch-case fallback。
func (p *ConfiguredCaptchaProvider) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || p.get(ctx, setting) != "1" {
		return out
	}
	provider := strings.ToLower(p.get(ctx, "captcha_provider"))
	adapter, found := captchaFromRegistry(provider, p.Settings, p.client())
	if !found {
		log.Printf("[captcha] 验证码供应商未注册：%s", provider)
		return out
	}
	return adapter.PublicConfig(ctx, scene)
}

// Verify 是服务器端唯一可信验证点。仅在已启用且完整配置的外部场景中执行。
// 统一走适配器注册表，不再有旧版 switch-case fallback。
func (p *ConfiguredCaptchaProvider) Verify(ctx context.Context, scene string, payload map[string]string, ip string) error {
	cfg := p.PublicConfig(ctx, scene)
	if !cfg.Enabled {
		return errors.New("外部人机验证未启用或配置不完整")
	}
	adapter, found := captchaFromRegistry(cfg.Provider, p.Settings, p.client())
	if !found {
		return errors.New("验证码供应商适配器未注册：" + cfg.Provider)
	}
	return adapter.Verify(ctx, scene, payload, ip)
}

func (p *ConfiguredCaptchaProvider) client() *http.Client {
	if p != nil && p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func readCaptchaResponse(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCaptchaResponseBytes+1))
	if err != nil || len(data) > maxCaptchaResponseBytes {
		return nil, errors.New("验证码响应无效")
	}
	return data, nil
}

func hexString(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&15]
	}
	return string(out)
}
