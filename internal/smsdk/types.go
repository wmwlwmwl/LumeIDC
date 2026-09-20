// Package smsdk 短信渠道开发包：类型、注册表与共享工具。
// 新增短信服务商 = 在 internal/plugins/sms/ 加一个适配器文件 + init 注册，核心零改动。
// 本包不依赖 service（避免环）；service 包经类型别名转发保持存量代码兼容。
package smsdk

import "context"

// Range 短信业务范围。
type Range string

const (
	RangeCN        Range = "cn"
	RangeGlobal    Range = "global"
	RangeMarketing Range = "marketing"
)

// AuditStatus 远程模板审核状态。
type AuditStatus string

const (
	AuditPending      AuditStatus = "pending"
	AuditApproved     AuditStatus = "approved"
	AuditRejected     AuditStatus = "rejected"
	AuditUnknown      AuditStatus = "unknown"
	AuditNotSupported AuditStatus = "not_supported"
)

// ProviderCapabilities 服务商能力声明。
type ProviderCapabilities struct {
	Ranges       []Range `json:"ranges"`
	OTP          bool    `json:"otp"`
	Notification bool    `json:"notification"`
	TemplateCRUD bool    `json:"template_crud"`
	AuditSync    bool    `json:"audit_sync"`
}

// ProviderDescriptor 服务商描述（后台配置表单与能力判定用）。
type ProviderDescriptor struct {
	Key          string               `json:"key"`
	Name         string               `json:"name"`
	ConfigFields []string             `json:"config_fields"`
	Capabilities ProviderCapabilities `json:"capabilities"`
}

// ProviderResult 发送/模板操作结果。
type ProviderResult struct {
	ProviderTemplateID                                  string
	Status                                              AuditStatus
	ProviderCode, Message, ProviderMessageID, RequestID string
}

// ProviderTemplate 模板视图（发送与远程模板操作共用）。
type ProviderTemplate struct {
	ID, Name, Content, SignName, Kind string
	Range                             Range
	Remark                            string
	Parameters                        map[string]string
}

// Provider 短信服务商适配器接口。
type Provider interface {
	Descriptor() ProviderDescriptor
	SendSMS(context.Context, string, ProviderTemplate, map[string]string) (ProviderResult, error)
	TemplateCreate(context.Context, ProviderTemplate) (ProviderResult, error)
	TemplateUpdate(context.Context, ProviderTemplate) (ProviderResult, error)
	TemplateQuery(context.Context, string, Range) (ProviderResult, error)
	TemplateDelete(context.Context, string, Range) (ProviderResult, error)
}
