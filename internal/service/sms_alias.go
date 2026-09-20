package service

import (
	"context"
	"net/http"
	"time"

	"lumeidc/internal/smsdk"
)

// 短信类型与注册表已迁 internal/smsdk（适配器可独立演进，见 internal/plugins/sms/）。
// 本文件为别名/转发平滑层：service 包内存量代码（sms_templates/sms_outbox/sms_routes 等）
// 与既有测试零改动。

type (
	SMSRange                = smsdk.Range
	SMSAuditStatus          = smsdk.AuditStatus
	SMSProviderCapabilities = smsdk.ProviderCapabilities
	ProviderDescriptor      = smsdk.ProviderDescriptor
	SMSProviderResult       = smsdk.ProviderResult
	SMSProviderTemplate     = smsdk.ProviderTemplate
	SMSProvider             = smsdk.Provider
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

func SMSProviderRegistry() map[string]ProviderDescriptor { return smsdk.Registry() }
func SMSProviderDescriptorFor(key string) (ProviderDescriptor, bool) {
	return smsdk.DescriptorFor(key)
}
func smsProviderSupports(key string, r SMSRange, kind string) bool {
	return smsdk.Supports(key, r, kind)
}
func smsProviderAuditNotSupported(provider string) SMSProviderResult {
	return smsdk.AuditNotSupportedResult(provider)
}
func smsHTTPSURL(raw, def string) (string, error) { return smsdk.HTTPSURL(raw, def) }
func tc3Signature(secretID, secretKey, host, action string, body []byte, now time.Time) string {
	return smsdk.TC3Signature(secretID, secretKey, host, action, body, now)
}
func md5Hex(v string) string                                { return smsdk.MD5Hex(v) }
func smsStringValue(v map[string]any, key string) string    { return smsdk.StringValue(v, key) }
func smsAudit(n string, base int) SMSAuditStatus            { return smsdk.Audit(n, base) }
func smsSign(s, c string) string                            { return smsdk.Sign(s, c) }
func smsPhoneForRange(p string, r SMSRange) (string, error) { return smsdk.PhoneForRange(p, r) }
func qcloudTemplateParams(v map[string]string) []string     { return smsdk.QcloudTemplateParams(v) }
func canonicalQuery(v map[string]string) string             { return smsdk.CanonicalQuery(v) }
func percentEncode(v string) string                         { return smsdk.PercentEncode(v) }
func hmacSHA256(k, v []byte) []byte                         { return smsdk.HMACSHA256(k, v) }
func sha256Hex(v []byte) string                             { return smsdk.SHA256Hex(v) }
func randomHex(size int) string                             { return smsdk.RandomHex(size) }

func NewSMSProvider(key string, settings map[string]string, client *http.Client) (SMSProvider, error) {
	return smsdk.NewProvider(key, settings, client)
}

func smsDoRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
	return smsdk.DoRequest(ctx, client, method, endpoint, body, headers)
}
func decodeSMSResponse(raw []byte) (map[string]any, error) { return smsdk.DecodeResponse(raw) }
func smsRejected() (SMSProviderResult, error)              { return smsdk.Rejected() }

var (
	smsIdentifier = smsdk.Identifier
)

func smsTextValid(s string, limit int) bool   { return smsdk.TextValid(s, limit) }
func allowedSMSURL(raw, provider string) bool { return smsdk.AllowedURL(raw, provider) }
