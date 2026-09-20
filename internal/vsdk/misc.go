package vsdk

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// StatusFromMap 从各家不一的响应里提取核验状态（status/verify_status/... 或嵌套 result）。
func StatusFromMap(m map[string]any) Status {
	candidates := []string{"status", "verify_status", "auth_status", "conclusion", "verify_result"}
	var value string
	for _, key := range candidates {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case string:
				value = strings.ToLower(strings.TrimSpace(x))
			case float64:
				value = strconv.Itoa(int(x))
			case bool:
				if x {
					value = "true"
				}
			}
			if value != "" {
				break
			}
		}
	}
	if result, ok := m["result"].(map[string]any); ok && value == "" {
		return StatusFromMap(result)
	}
	switch value {
	case "success", "passed", "pass", "approved", "1", "true":
		return Status{Status: "approved"}
	case "failed", "failure", "rejected", "2", "-1", "false":
		return Status{Status: "rejected"}
	default:
		return Status{Status: "pending"}
	}
}

// ResponseOK 各家响应成功判定（status/code 200/20000/1/ok，或 success=true）。
func ResponseOK(m map[string]any) bool {
	if m == nil {
		return false
	}
	known := false
	for _, key := range []string{"status", "code"} {
		if raw, exists := m[key]; exists {
			known = true
			switch v := raw.(type) {
			case float64:
				return v == 200 || v == 20000 || v == 1
			case string:
				return v == "200" || v == "20000" || strings.EqualFold(v, "ok") || v == "1"
			}
		}
	}
	if v, ok := m["success"].(bool); ok {
		return v
	}
	if known {
		return false
	}
	return false
}

// StringValue 安全取响应字符串字段。
func StringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

// FirstString 按序取首个非空字符串字段。
func FirstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := StringValue(m, key); v != "" {
			return v
		}
	}
	return ""
}

// SHA256Hex 计算 SHA-256 并输出十六进制小写。
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// RandomHex 生成 n 字节随机数的十六进制串；熵源失败返回空串（调用方按错误处理）。
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
