package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SMSRange string

const (
	SMSRangeCN        SMSRange = "cn"
	SMSRangeGlobal    SMSRange = "global"
	SMSRangeMarketing SMSRange = "marketing"
)

type SMSAuditStatus string

const (
	SMSAuditPending      SMSAuditStatus = "pending"
	SMSAuditApproved     SMSAuditStatus = "approved"
	SMSAuditRejected     SMSAuditStatus = "rejected"
	SMSAuditUnknown      SMSAuditStatus = "unknown"
	SMSAuditNotSupported SMSAuditStatus = "not_supported"
)

type SMSProviderCapabilities struct {
	Ranges       []SMSRange `json:"ranges"`
	OTP          bool       `json:"otp"`
	Notification bool       `json:"notification"`
	TemplateCRUD bool       `json:"template_crud"`
	AuditSync    bool       `json:"audit_sync"`
}
type ProviderDescriptor struct {
	Key          string                  `json:"key"`
	Name         string                  `json:"name"`
	ConfigFields []string                `json:"config_fields"`
	Capabilities SMSProviderCapabilities `json:"capabilities"`
}
type SMSProviderResult struct {
	ProviderTemplateID                                  string
	Status                                              SMSAuditStatus
	ProviderCode, Message, ProviderMessageID, RequestID string
}
type SMSProviderTemplate struct {
	ID, Name, Content, SignName, Kind string
	Range                             SMSRange
	Remark                            string
	Parameters                        map[string]string
}
type SMSProvider interface {
	Descriptor() ProviderDescriptor
	SendSMS(context.Context, string, SMSProviderTemplate, map[string]string) (SMSProviderResult, error)
	TemplateCreate(context.Context, SMSProviderTemplate) (SMSProviderResult, error)
	TemplateUpdate(context.Context, SMSProviderTemplate) (SMSProviderResult, error)
	TemplateQuery(context.Context, string, SMSRange) (SMSProviderResult, error)
	TemplateDelete(context.Context, string, SMSRange) (SMSProviderResult, error)
}

var smsProviderDescriptors = map[string]ProviderDescriptor{
	"aliyun":      {"aliyun", "阿里云号码认证", []string{"sms_access_key", "sms_secret_key", "sms_sign_name"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN}, true, false, false, false}},
	"aliyun_sms":  {"aliyun_sms", "阿里云短信", []string{"sms_access_key", "sms_secret_key", "sms_sign_name", "sms_endpoint"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN, SMSRangeGlobal}, true, true, true, true}},
	"qcloudsms":   {"qcloudsms", "腾讯云短信", []string{"sms_access_key", "sms_secret_key", "sms_username", "sms_sign_name", "sms_region"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN, SMSRangeGlobal, SMSRangeMarketing}, true, true, true, true}},
	"submail":     {"submail", "赛邮", []string{"sms_access_key", "sms_secret_key", "sms_sign_name", "sms_global_access_key", "sms_global_secret_key", "sms_global_sign_name"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN, SMSRangeGlobal}, true, true, true, true}},
	"smsbao":      {"smsbao", "短信宝", []string{"sms_username", "sms_secret_key", "sms_sign_name"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN, SMSRangeGlobal}, true, true, false, false}},
	"idcsmart":    {"idcsmart", "智简魔方", []string{"sms_username", "sms_secret_key", "sms_sign_name"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN}, true, true, true, true}},
	"idcsmartpro": {"idcsmartpro", "智简魔方国内营销", []string{"sms_username", "sms_secret_key", "sms_sign_name"}, SMSProviderCapabilities{[]SMSRange{SMSRangeMarketing}, false, true, true, true}},
	"stay33":      {"stay33", "Stay33", []string{"sms_username", "sms_secret_key", "sms_sign_name", "sms_endpoint"}, SMSProviderCapabilities{[]SMSRange{SMSRangeCN}, true, true, false, false}},
}

func SMSProviderRegistry() map[string]ProviderDescriptor {
	out := make(map[string]ProviderDescriptor, len(smsProviderDescriptors))
	for k, v := range smsProviderDescriptors {
		out[k] = v
	}
	return out
}
func SMSProviderDescriptorFor(key string) (ProviderDescriptor, bool) {
	d, ok := smsProviderDescriptors[strings.ToLower(strings.TrimSpace(key))]
	return d, ok
}
func smsProviderSupports(key string, r SMSRange, kind string) bool {
	if r == "" {
		r = SMSRangeCN
	}
	d, ok := SMSProviderDescriptorFor(key)
	if !ok || (r == SMSRangeMarketing && kind == "otp") {
		return false
	}
	for _, x := range d.Capabilities.Ranges {
		if x == r {
			return kind == "otp" && d.Capabilities.OTP || kind == "notification" && d.Capabilities.Notification
		}
	}
	return false
}
func qcloudTemplateParams(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(keys[i], "param"))
		b, _ := strconv.Atoi(strings.TrimPrefix(keys[j], "param"))
		return a < b
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, values[k])
	}
	return out
}
func smsProviderAuditNotSupported(provider string) SMSProviderResult {
	return SMSProviderResult{Status: SMSAuditNotSupported, ProviderCode: "not_supported", Message: "该服务商不支持此范围的远程模板操作"}
}
func smsHTTPSURL(raw, defaultURL string) (string, error) {
	if raw == "" {
		raw = defaultURL
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	d, _ := url.Parse(defaultURL)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Host != d.Host || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/" && u.Path != d.Path) {
		return "", errors.New("短信接口必须使用供应商固定 HTTPS 地址")
	}
	return defaultURL, nil
}
func tc3Signature(secretID, secretKey, host, action string, body []byte, now time.Time) string {
	date := now.UTC().Format("2006-01-02")
	canonical := "POST\n/\n\ncontent-type:application/json; charset=utf-8\nhost:" + host + "\n\ncontent-type;host\n" + sha256Hex(body)
	credential := date + "/sms/tc3_request"
	text := "TC3-HMAC-SHA256\n" + strconv.FormatInt(now.Unix(), 10) + "\n" + credential + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("TC3"+secretKey), []byte(date))
	key = hmacSHA256(key, []byte("sms"))
	key = hmacSHA256(key, []byte("tc3_request"))
	return fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%s", secretID, credential, hex.EncodeToString(hmacSHA256(key, []byte(text))))
}
func md5Hex(v string) string { h := md5.Sum([]byte(v)); return hex.EncodeToString(h[:]) }
func smsStringValue(v map[string]any, key string) string {
	switch x := v[key].(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}
func smsAudit(number string, base int) SMSAuditStatus {
	n, e := strconv.Atoi(number)
	if e != nil {
		return SMSAuditUnknown
	}
	switch n - base {
	case 0:
		return SMSAuditPending
	case 1:
		return SMSAuditApproved
	case 2:
		return SMSAuditRejected
	}
	return SMSAuditUnknown
}
func smsSign(sign, content string) string {
	sign = strings.Trim(sign, "【】")
	if sign == "" || strings.HasPrefix(content, "【"+sign+"】") {
		return content
	}
	return "【" + sign + "】" + content
}
func smsPhoneForRange(phone string, r SMSRange) (string, error) {
	if r == "" {
		r = SMSRangeCN
	}
	if r != SMSRangeGlobal {
		normalized, e := NormalizePhone(phone)
		if e != nil {
			return "", e
		}
		if !strings.HasPrefix(normalized, "+86") || !mainlandPhone.MatchString(strings.TrimPrefix(normalized, "+86")) {
			return "", errors.New("国内模板仅支持中国大陆手机号")
		}
		return normalized, nil
	}
	if len(phone) < 8 || len(phone) > 16 || phone[0] != '+' || phone[1] == '0' || strings.HasPrefix(phone, "+86") {
		return "", errors.New("国际模板须使用非中国大陆的 E.164 手机号码")
	}
	for _, c := range phone[1:] {
		if c < '0' || c > '9' {
			return "", errors.New("国际手机号格式无效")
		}
	}
	return phone, nil
}
