package captcha

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lumeidc/internal/csdk"
)

// 极验第四代（GT4）验证码适配器。
type geetestAdapter struct{ host *csdk.Host }

var geetestDescriptor = csdk.Descriptor{
	Key:        "geetest",
	Name:       "极验 Geetest",
	SDKURL:     "https://static.geetest.com/v4/gt4.js",
	SDKVersion: "v4",
	Fields: []csdk.ConfigField{
		{Key: "captcha_geetest_id", Label: "Geetest ID"},
		{Key: "captcha_geetest_key", Label: "Geetest Key", Secret: true},
	},
}

// PublicConfig 返回暴露给浏览器的极验安全配置。
func (a *geetestAdapter) PublicConfig(ctx context.Context, scene string) csdk.PublicConfig {
	publicID, secret := a.host.Get(ctx, "captcha_geetest_id"), a.host.Get(ctx, "captcha_geetest_key")
	if publicID == "" || secret == "" {
		return csdk.PublicConfig{Scene: scene}
	}
	cfg := geetestDescriptor.BaseConfig(publicID, scene)
	cfg.Enabled = true
	return cfg
}

// Verify 是极验第四代的服务器端验证。
func (a *geetestAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	id, key := a.host.Get(ctx, "captcha_geetest_id"), a.host.Get(ctx, "captcha_geetest_key")
	lot, output, pass, gen := v["lot_number"], v["captcha_output"], v["pass_token"], v["gen_time"]
	if id == "" || key == "" || lot == "" || output == "" || pass == "" || gen == "" {
		return errors.New("请完成验证码")
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(lot))
	form := url.Values{"lot_number": {lot}, "captcha_output": {output}, "pass_token": {pass}, "gen_time": {gen}, "captcha_id": {id}, "sign_token": {csdk.HexString(mac.Sum(nil))}}
	if ip != "" {
		form.Set("user_ip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gcaptcha4.geetest.com/validate", strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.host.HTTPClient().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	body, err := csdk.ReadResponse(resp)
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
	csdk.Register(geetestDescriptor, func(h *csdk.Host) (csdk.Provider, error) {
		return &geetestAdapter{host: h}, nil
	})
}
