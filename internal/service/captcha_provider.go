package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
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
	case "login":
		return "external_captcha_login_enabled"
	case "phone_login_code":
		return "external_captcha_phone_login_code_enabled"
	default:
		return ""
	}
}

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
func (p *ConfiguredCaptchaProvider) PublicConfig(ctx context.Context, scene string) CaptchaPublicConfig {
	out := CaptchaPublicConfig{Scene: scene}
	setting := externalCaptchaSetting(scene)
	if setting == "" || p.get(ctx, setting) != "1" {
		return out
	}
	provider := strings.ToLower(p.get(ctx, "captcha_provider"))
	sdkURL, _ := captchaSDK(provider)
	if sdkURL == "" {
		return out
	}
	var publicID, secret string
	switch provider {
	case "geetest":
		publicID, secret = p.get(ctx, "captcha_geetest_id"), p.get(ctx, "captcha_geetest_key")
	case "vaptcha":
		publicID, secret = p.get(ctx, "captcha_vaptcha_vid"), p.get(ctx, "captcha_vaptcha_key")
	case "corptcha":
		publicID, secret = p.get(ctx, "captcha_corptcha_site_key"), p.get(ctx, "captcha_corptcha_secret")
	}
	if publicID == "" || secret == "" {
		return out
	}
	configured := captchaPublicProviderConfig(provider, publicID, scene)
	configured.Enabled = true
	return configured
}

// Verify 是服务器端唯一可信验证点。仅在已启用且完整配置的外部场景中执行。
func (p *ConfiguredCaptchaProvider) Verify(ctx context.Context, scene string, payload map[string]string, ip string) error {
	cfg := p.PublicConfig(ctx, scene)
	if !cfg.Enabled {
		return errors.New("外部人机验证未启用或配置不完整")
	}
	switch cfg.Provider {
	case "geetest":
		return p.verifyGeetest(ctx, payload, ip)
	case "vaptcha":
		return p.verifyVaptcha(ctx, payload, ip)
	case "corptcha":
		return p.verifyCorptcha(ctx, scene, payload)
	default:
		return errors.New("验证码 provider 未配置或不受支持")
	}
}

func (p *ConfiguredCaptchaProvider) verifyGeetest(ctx context.Context, v map[string]string, ip string) error {
	id, key := p.get(ctx, "captcha_geetest_id"), p.get(ctx, "captcha_geetest_key")
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
	return p.postForm(ctx, "https://gcaptcha4.geetest.com/validate", form, func(body []byte) error {
		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(body, &out) != nil || out.Result != "success" {
			return errors.New("验证码校验失败")
		}
		return nil
	})
}

func (p *ConfiguredCaptchaProvider) verifyVaptcha(ctx context.Context, v map[string]string, ip string) error {
	vid, key := p.get(ctx, "captcha_vaptcha_vid"), p.get(ctx, "captcha_vaptcha_key")
	if vid == "" || key == "" || v["token"] == "" || v["knock"] == "" {
		return errors.New("请完成验证码")
	}
	// 优先使用前端 SDK 返回的签名 IP（与 token 签名一致），否则回退到请求 IP。
	if v["ip"] != "" {
		ip = v["ip"]
	}
	body, _ := json.Marshal(map[string]string{"vid": vid, "vkey": key, "token": v["token"], "knock": v["knock"], "dfu": v["dfu"], "ip": ip})
	return p.postJSON(ctx, "https://v41.vaptcha.com/api/verify", body, func(data []byte) error {
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
	})
}

func (p *ConfiguredCaptchaProvider) verifyCorptcha(ctx context.Context, scene string, v map[string]string) error {
	secret, site := p.get(ctx, "captcha_corptcha_secret"), p.get(ctx, "captcha_corptcha_site_key")
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
	resp, err := p.client().Do(req)
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

func (p *ConfiguredCaptchaProvider) postForm(ctx context.Context, endpoint string, form url.Values, check func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	body, err := readCaptchaResponse(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("验证码校验失败")
	}
	return check(body)
}

func (p *ConfiguredCaptchaProvider) postJSON(ctx context.Context, endpoint string, body []byte, check func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("验证码请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return errors.New("验证码服务不可用")
	}
	defer resp.Body.Close()
	data, err := readCaptchaResponse(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("验证码校验失败")
	}
	return check(data)
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
