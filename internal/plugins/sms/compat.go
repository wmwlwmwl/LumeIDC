// Package sms 内置短信服务商适配器（接口类扩展，与供应商同轨道）。
// 新增服务商：本目录加适配器文件 + init 中 smsdk.RegisterSMSProvider 注册，
// 并在 internal/plugins/all/all.go 无需改动（本包已聚合）。核心零改动。
package sms

import "lumeidc/internal/smsdk"

// 迁移兼容层：从 service 包迁入的适配器沿用旧符号名（经 smsdk 转发，行为不变）。
// 新增适配器请直接使用 smsdk.Xxx，勿再扩散旧名。

type (
	SMSProvider             = smsdk.Provider
	ProviderDescriptor      = smsdk.ProviderDescriptor
	SMSProviderResult       = smsdk.ProviderResult
	SMSProviderTemplate     = smsdk.ProviderTemplate
	SMSRange                = smsdk.Range
	SMSAuditStatus          = smsdk.AuditStatus
	SMSProviderCapabilities = smsdk.ProviderCapabilities
	smsUnknownError         = smsdk.UnknownError
)

const (
	SMSRangeCN        = smsdk.RangeCN
	SMSRangeGlobal    = smsdk.RangeGlobal
	SMSRangeMarketing = smsdk.RangeMarketing

	SMSAuditPending      = smsdk.AuditPending
	SMSAuditApproved     = smsdk.AuditApproved
	SMSAuditRejected     = smsdk.AuditRejected
	SMSAuditUnknown      = smsdk.AuditUnknown
	SMSAuditNotSupported = smsdk.AuditNotSupported
)

var (
	smsDoRequest                 = smsdk.DoRequest
	smsHTTPSURL                  = smsdk.HTTPSURL
	decodeSMSResponse            = smsdk.DecodeResponse
	smsRejected                  = smsdk.Rejected
	smsProviderSupports          = smsdk.Supports
	smsProviderAuditNotSupported = smsdk.AuditNotSupportedResult
	smsStringValue               = smsdk.StringValue
	smsPhoneForRange             = smsdk.PhoneForRange
	smsAudit                     = smsdk.Audit
	smsSign                      = smsdk.Sign
	md5Hex                       = smsdk.MD5Hex
	hmacSHA256                   = smsdk.HMACSHA256
	sha256Hex                    = smsdk.SHA256Hex
	randomHex                    = smsdk.RandomHex
	canonicalQuery               = smsdk.CanonicalQuery
	percentEncode                = smsdk.PercentEncode
	tc3Signature                 = smsdk.TC3Signature
	qcloudTemplateParams         = smsdk.QcloudTemplateParams
	smsTextValid                 = smsdk.TextValid
	smsIdentifier                = smsdk.Identifier
	allowedSMSURL                = smsdk.AllowedURL
	SMSProviderDescriptorFor     = smsdk.DescriptorFor
)
