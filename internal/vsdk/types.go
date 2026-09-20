// Package vsdk 实名渠道开发包：类型、注册表与适配器宿主。
// 新增实名服务商 = 在 internal/plugins/verify/ 加一个适配器文件 + init 注册，核心零改动。
// 本包不依赖 service（避免环）；service 包经类型别名转发保持存量代码兼容。
package vsdk

import "context"

// Provider 第三方实名服务的最小边界。provider 不得直接修改用户表。
type Provider interface {
	Key() string
	Start(context.Context, StartRequest) (StartResult, error)
	Poll(context.Context, string) (Status, error)
}

// StartRequest 发起实名请求（LegalName/IdentityNumber 已在上游流程校验）。
type StartRequest struct {
	LegalName      string
	IdentityNumber string
	ReturnURL      string
}

// StartResult 发起结果：ProviderRef 供 Poll 回查，URL 为用户跳转的认证页。
type StartResult struct {
	ProviderRef string
	URL         string
}

// Status 实名核验状态。
type Status struct {
	Status  string // pending, approved, rejected, failed
	Message string
}

// ConfigField 服务商配置字段声明（后台实名设置页动态渲染用）。
// Key 为 settings 表完整键名（如 verification_xxx_api_key）；Secret 字段保存时留空表示不修改、读取时不回传。
type ConfigField struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Secret bool   `json:"secret,omitempty"`
}

// Descriptor 服务商描述（后台下拉与配置表单用）。
type Descriptor struct {
	Key    string        `json:"key"`
	Name   string        `json:"name"`
	Fields []ConfigField `json:"fields"`
	// ReturnHost 认证回跳地址的 host 白名单（StartProvider 强制校验插件返回 URL 的 host）。
	// 新增渠道必须声明，否则其返回地址一律被拒（fail-closed）。不下发前端。
	ReturnHost string `json:"-"`
}
