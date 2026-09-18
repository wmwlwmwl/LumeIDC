package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// corptchaCaptchaAdapter 25y Corptcha 验证码适配器。
type corptchaCaptchaAdapter struct {
	Settings *repo.Settings
	Client   *http.Client
}

func newCorptchaAdapter(settings *repo.Settings, client *http.Client) CaptchaProvider {
	return &corptchaCaptchaAdapter{Settings: settings, Client: client}
}

func (a *corptchaCaptchaAdapter) get(ctx context.Context, key string) string {
	if a == nil || a.Settings == nil {
		return ""
	}
	v, _ := a.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

func (a *corptchaCaptchaAdapter) client() *http.Client {
	if a != nil && a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ExternalRequested 表示当前场景是否启用了外部人机验证。
func (a *corptchaCaptchaAdapter) ExternalRequested(ctx context.Context, scene string) bool {
	return externalCaptchaSetting(scene) != "" && a.get(ctx, externalCaptchaSetting(scene)) == "1"
}

// PublicConfig 返回暴露给浏览器的 Corptcha 安全配置。
func (a *corptchaCaptchaAdapter) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || a.get(ctx, setting) != "1" {
		return out
	}
	sdkURL, _ := captchaSDK("corptcha")
	if sdkURL == "" {
		return out
	}
	publicID, secret := a.get(ctx, "captcha_corptcha_site_key"), a.get(ctx, "captcha_corptcha_secret")
	if publicID == "" || secret == "" {
		return out
	}
	configured := captchaPublicProviderConfig("corptcha", publicID, scene)
	configured.Enabled = true
	return configured
}

// Verify 是 Corptcha 的服务器端验证。
func (a *corptchaCaptchaAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	secret, site := a.get(ctx, "captcha_corptcha_secret"), a.get(ctx, "captcha_corptcha_site_key")
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
	resp, err := a.client().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	data, err := readCaptchaResponse(resp)
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
	registerCaptchaAdapter("corptcha", newCorptchaAdapter)
}
