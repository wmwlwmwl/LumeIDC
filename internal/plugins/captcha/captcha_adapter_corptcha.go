package captcha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"lumeidc/internal/csdk"
)

// 25y Corptcha 验证码适配器。
type corptchaAdapter struct{ host *csdk.Host }

var corptchaDescriptor = csdk.Descriptor{
	Key:        "corptcha",
	Name:       "Corptcha",
	SDKURL:     "https://res.25y.cn/corptcha/corptcha.iife.js",
	SDKVersion: "v1",
	RenderMode: "inline",
	APIBaseURL: "https://cpt-api.25y.cn",
	Fields: []csdk.ConfigField{
		{Key: "captcha_corptcha_site_key", Label: "Corptcha Site Key"},
		{Key: "captcha_corptcha_secret", Label: "Corptcha Secret", Secret: true},
	},
}

// PublicConfig 返回暴露给浏览器的 Corptcha 安全配置。
func (a *corptchaAdapter) PublicConfig(ctx context.Context, scene string) csdk.PublicConfig {
	publicID, secret := a.host.Get(ctx, "captcha_corptcha_site_key"), a.host.Get(ctx, "captcha_corptcha_secret")
	if publicID == "" || secret == "" {
		return csdk.PublicConfig{Scene: scene}
	}
	cfg := corptchaDescriptor.BaseConfig(publicID, scene)
	// Corptcha 按场景声明 purpose（与 Verify 提交的 purpose 一致）。
	cfg.Purpose = scene
	cfg.Enabled = true
	return cfg
}

// Verify 是 Corptcha 的服务器端验证。
func (a *corptchaAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	secret, site := a.host.Get(ctx, "captcha_corptcha_secret"), a.host.Get(ctx, "captcha_corptcha_site_key")
	if secret == "" || site == "" || v["token"] == "" {
		return errors.New("请完成验证码")
	}
	body, _ := json.Marshal(map[string]string{"token": v["token"], "purpose": scene, "siteKey": site})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://cpt-api.25y.cn/v1/verify", bytes.NewReader(body))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.host.HTTPClient().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	data, err := csdk.ReadResponse(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("验证码校验失败")
	}
	var out struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(data, &out) != nil || !out.Success {
		return errors.New("验证码校验失败")
	}
	return nil
}

func init() {
	csdk.Register(corptchaDescriptor, func(h *csdk.Host) (csdk.Provider, error) {
		return &corptchaAdapter{host: h}, nil
	})
}
