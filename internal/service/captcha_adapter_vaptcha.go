package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// vaptchaCaptchaAdapter Vaptcha 手势验证码适配器。
type vaptchaCaptchaAdapter struct {
	Settings *repo.Settings
	Client   *http.Client
}

func newVaptchaAdapter(settings *repo.Settings, client *http.Client) CaptchaProvider {
	return &vaptchaCaptchaAdapter{Settings: settings, Client: client}
}

func (a *vaptchaCaptchaAdapter) get(ctx context.Context, key string) string {
	if a == nil || a.Settings == nil {
		return ""
	}
	v, _ := a.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

func (a *vaptchaCaptchaAdapter) client() *http.Client {
	if a != nil && a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ExternalRequested 表示当前场景是否启用了外部人机验证。
func (a *vaptchaCaptchaAdapter) ExternalRequested(ctx context.Context, scene string) bool {
	return externalCaptchaSetting(scene) != "" && a.get(ctx, externalCaptchaSetting(scene)) == "1"
}

// PublicConfig 返回暴露给浏览器的 Vaptcha 安全配置。
func (a *vaptchaCaptchaAdapter) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || a.get(ctx, setting) != "1" {
		return out
	}
	sdkURL, _ := captchaSDK("vaptcha")
	if sdkURL == "" {
		return out
	}
	publicID, secret := a.get(ctx, "captcha_vaptcha_vid"), a.get(ctx, "captcha_vaptcha_key")
	if publicID == "" || secret == "" {
		return out
	}
	configured := captchaPublicProviderConfig("vaptcha", publicID, scene)
	configured.Enabled = true
	return configured
}

// Verify 是 Vaptcha 的服务器端验证。
func (a *vaptchaCaptchaAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	vid, key := a.get(ctx, "captcha_vaptcha_vid"), a.get(ctx, "captcha_vaptcha_key")
	if vid == "" || key == "" || v["token"] == "" || v["knock"] == "" {
		return errors.New("请完成验证码")
	}
	// 优先使用前端 SDK 返回的签名 IP（与 token 签名一致），否则回退到请求 IP。
	if v["ip"] != "" {
		ip = v["ip"]
	}
	body, _ := json.Marshal(map[string]string{"vid": vid, "vkey": key, "token": v["token"], "knock": v["knock"], "dfu": v["dfu"], "ip": ip})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://v41.vaptcha.com/api/verify", bytes.NewReader(body))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	data, err := readCaptchaResponse(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("验证码校验失败")
	}
	// 严格成功条件：外层 code=0 且 data.result=true 且 data.code=0（data.code: 1=token 存在但校验失败）。
	var out struct {
		Code int `json:"code"`
		Data struct {
			Code   int  `json:"code"`
			Result bool `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &out) != nil || out.Code != 0 || !out.Data.Result || out.Data.Code != 0 {
		log.Printf("[captcha] vaptcha 校验失败 响应=%s", data)
		return errors.New("验证码校验失败")
	}
	return nil
}

func init() {
	registerCaptchaAdapter("vaptcha", newVaptchaAdapter)
}
