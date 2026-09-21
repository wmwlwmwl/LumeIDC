package captcha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"lumeidc/internal/csdk"
)

// Vaptcha 手势验证码适配器。
type vaptchaAdapter struct{ host *csdk.Host }

var vaptchaDescriptor = csdk.Descriptor{
	Key:        "vaptcha",
	Name:       "Vaptcha",
	SDKURL:     "https://cdn4.vaptcha.com/src/v4.js",
	SDKVersion: "v4",
	Fields: []csdk.ConfigField{
		{Key: "captcha_vaptcha_vid", Label: "Vaptcha VID"},
		{Key: "captcha_vaptcha_key", Label: "Vaptcha Key", Secret: true},
	},
}

// PublicConfig 返回暴露给浏览器的 Vaptcha 安全配置。
func (a *vaptchaAdapter) PublicConfig(ctx context.Context, scene string) csdk.PublicConfig {
	publicID, secret := a.host.Get(ctx, "captcha_vaptcha_vid"), a.host.Get(ctx, "captcha_vaptcha_key")
	if publicID == "" || secret == "" {
		return csdk.PublicConfig{Scene: scene}
	}
	cfg := vaptchaDescriptor.BaseConfig(publicID, scene)
	cfg.Enabled = true
	return cfg
}

// Verify 是 Vaptcha 的服务器端验证。
func (a *vaptchaAdapter) Verify(ctx context.Context, scene string, v map[string]string, ip string) error {
	vid, key := a.host.Get(ctx, "captcha_vaptcha_vid"), a.host.Get(ctx, "captcha_vaptcha_key")
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
	resp, err := a.host.HTTPClient().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	data, err := csdk.ReadResponse(resp)
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
	csdk.Register(vaptchaDescriptor, func(h *csdk.Host) (csdk.Provider, error) {
		return &vaptchaAdapter{host: h}, nil
	})
}
