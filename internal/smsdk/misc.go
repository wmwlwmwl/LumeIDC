package smsdk

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AuditNotSupportedResult 服务商不支持远程模板操作的统一结果。
// （原名 AuditNotSupported 与状态常量撞名，故加 Result 后缀。）
func AuditNotSupportedResult(provider string) ProviderResult {
	return ProviderResult{Status: AuditNotSupported, ProviderCode: "not_supported", Message: "该服务商不支持此范围的远程模板操作"}
}

// Audit 把供应商的审核状态数字（base 起 0/1/2 映射 pending/approved/rejected）转为统一状态。
func Audit(number string, base int) AuditStatus {
	n, e := strconv.Atoi(number)
	if e != nil {
		return AuditUnknown
	}
	switch n - base {
	case 0:
		return AuditPending
	case 1:
		return AuditApproved
	case 2:
		return AuditRejected
	}
	return AuditUnknown
}

// Sign 内容加签名前缀（已带签名则原样）。
func Sign(sign, content string) string {
	sign = strings.Trim(sign, "【】")
	if sign == "" || strings.HasPrefix(content, "【"+sign+"】") {
		return content
	}
	return "【" + sign + "】" + content
}

var (
	mainlandPhone = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
	// intlPhone：E.164 — + 开头、首位非 0 的国家码（1-3 位）+ 国内号码，数字总数 7-15 位。
	intlPhone = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

// NormalizePhone 手机号规范化（+86/E.164）。与 service.NormalizePhone 同源：
// 本包自含一份以避免 smsdk → service 反向依赖。
func NormalizePhone(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	if phone == "" {
		return "", errors.New("手机号格式不正确")
	}
	if strings.HasPrefix(phone, "+") {
		if !intlPhone.MatchString(phone) {
			return "", errors.New("手机号格式不正确")
		}
		return phone, nil
	}
	// 无名前缀按中国大陆处理（兼容历史录入/内部输入）
	if strings.HasPrefix(phone, "86") && len(phone) == 13 {
		phone = phone[2:]
	}
	if !mainlandPhone.MatchString(phone) {
		return "", errors.New("手机号格式不正确")
	}
	return "+86" + phone, nil
}

// PhoneForRange 按业务范围校验手机号：国内模板仅大陆号，国际模板须非大陆 E.164。
func PhoneForRange(phone string, r Range) (string, error) {
	if r == "" {
		r = RangeCN
	}
	if r != RangeGlobal {
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

// HTTPSURL 校验供应商自定义端点（须与默认地址同 host 的 HTTPS，防 SSRF）。
func HTTPSURL(raw, defaultURL string) (string, error) {
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

// StringValue 从 JSON map 取字符串（兼容 json.Number）。
func StringValue(v map[string]any, key string) string {
	switch x := v[key].(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

// TextValid 文本合法性：UTF-8 有效 + 限长 + 无控制/格式字符（模板名/内容/签名共用）。
func TextValid(s string, limit int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) < 0
}

// Identifier 模板编码/签名等标识符白名单正则。
var Identifier = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// AllowedURL 供应商自定义端点域名白名单（SSRF 防护；适配器侧用）。
func AllowedURL(raw, provider string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Port() != "" || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch provider {
	case "aliyun":
		return host == "dypnsapi.aliyuncs.com"
	case "aliyun_sms":
		return host == "dysmsapi.aliyuncs.com"
	case "stay33":
		return host == "idc.stay33.cn" || host == "api.freescdn.com"
	default:
		return false
	}
}

// QcloudTemplateParams 腾讯云模板参数排序（param1..N → 值有序数组）。
func QcloudTemplateParams(values map[string]string) []string {
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
