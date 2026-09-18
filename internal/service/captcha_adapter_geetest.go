package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// geetestCaptchaAdapter 极验第四代（GT4）验证码适配器。
type geetestCaptchaAdapter struct {
	Settings *repo.Settings
	Client   *http.Client
}

func newGeetestAdapter(settings *repo.Settings, client *http.Client) CaptchaProvider {
	return &geetestCaptchaAdapter{Settings: settings, Client: client}
}

func (a *geetestCaptchaAdapter) get(ctx context.Context, key string) string {
	if a == nil || a.Settings == nil {
		return ""
	}
	v, _ := a.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

func (a *geetestCaptchaAdapter) client() *http.Client {
	if a != nil && a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ExternalRequested 表示当前场景是否启用了外部人机验证。
func (a *geetestCaptchaAdapter) ExternalRequested(ctx context.Context, scene string) bool {
	return externalCaptchaSetting(scene) != "" && a.get(ctx, externalCaptchaSetting(scene)) == "1"
}

// PublicConfig 返回暴露给浏览器的极验安全配置。
func (a *geetestCaptchaAdapter) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || a.get(ctx, setting) != "1" {
		return out
	}
	sdkURL, _ := captchaSDK("geetest")
	if sdkURL == "" {
		return out
	}
	publicID, secret := a.get(ctx, "captcha_geetest_id"), a.get(ctx, "captcha_geetest_key")
	if publicID == "" || secret == "" {
		return out
	}
	configured := captchaPublicProviderConfig("geetest", publicID, scene)
	configured.Enabled = true
	return configured
}

// Verify 是极验第四代的服务器端验证。
func (a *geetestCaptchaAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	id, key := a.get(ctx, "captcha_geetest_id"), a.get(ctx, "captcha_geetest_key")
	lot, output, pass, gen := v["lot_number"], v["captcha_output"], v["pass_token"], v["gen_time"]
	if id == "" || key == "" || lot == "" || output == "" || pass == "" || gen == "" {
		return errors.New("请完成验证码")
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(lot))
	form := url.Values{"lot_number": {lot}, "captcha_output": {output}, "pass_token": {pass}, "gen_time": {gen}, "captcha_id": {id}, "sign_token": {hexString(mac.Sum(nil))}}
	if ip != "" {
		form.Set("user_ip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gcaptcha4.geetest.com/validate", strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	body, err := readCaptchaResponse(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("验证码校验失败")
	}
	var out struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(body, &out) != nil || out.Result != "success" {
		return errors.New("验证码校验失败")
	}
	return nil
}

func init() {
	registerCaptchaAdapter("geetest", newGeetestAdapter)
}
