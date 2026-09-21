// Package csdk 外部人机验证开发包：类型、注册表与适配器宿主。
// 新增验证码供应商 = 在 internal/plugins/captcha/ 加一个适配器文件 + init 注册，核心零改动。
// 本包不依赖 service（避免环）；service 包经类型别名转发保持存量代码兼容。
package csdk

import "context"

// Provider 外部人机验证供应商的最小边界。
// 场景开关（哪个场景启用外部验证）是系统层逻辑，不属于本接口。
type Provider interface {
	// PublicConfig 返回暴露给浏览器的外部验证配置（未启用或配置不完整时返回 Enabled=false）。
	PublicConfig(ctx context.Context, scene string) PublicConfig
	// Verify 服务器端校验：payload 为前端 SDK 回传的字段，ip 为客户端 IP。
	Verify(ctx context.Context, scene string, payload map[string]string, ip string) error
}

// PublicConfig 是唯一允许暴露给浏览器的外部验证码信息。
type PublicConfig struct {
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

// ConfigField 供应商配置字段声明（后台安全设置页动态渲染用）。
// Key 为 settings 表完整键名（如 captcha_xxx_key）；Secret 字段保存时留空表示不修改、读取时不回传。
type ConfigField struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Secret bool   `json:"secret,omitempty"`
}

// Descriptor 供应商描述（后台下拉、配置表单与公开配置构造用）。
type Descriptor struct {
	Key    string        `json:"key"`
	Name   string        `json:"name"`
	Fields []ConfigField `json:"fields"`
	// 前端 SDK 元数据（公开配置由此构造，替代原 captchaSDK 硬编码 switch）。
	SDKURL     string `json:"sdk_url,omitempty"`
	SDKVersion string `json:"sdk_version,omitempty"`
	RenderMode string `json:"render_mode,omitempty"`
	APIBaseURL string `json:"api_base_url,omitempty"`
}

// BaseConfig 由描述符构造公开配置骨架（Enabled 由适配器在配置完整时置 true）。
func (d Descriptor) BaseConfig(publicID, scene string) PublicConfig {
	return PublicConfig{
		Scene:      scene,
		Provider:   d.Key,
		PublicID:   publicID,
		SDKURL:     d.SDKURL,
		ScriptURL:  d.SDKURL,
		SDKVersion: d.SDKVersion,
		RenderMode: d.RenderMode,
		APIBaseURL: d.APIBaseURL,
	}
}
