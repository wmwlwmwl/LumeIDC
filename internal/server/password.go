package server

import (
	cryptorand "crypto/rand"
	"strings"
)

// 主机密码允许字符集（大写+小写+数字+常见特殊符号），各上游通用。
const hostPasswordChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789~!@#$&*()_-+="

// hostPasswordAllowed 输入校验用的完整允许字符集（比生成集宽，兼容上游完整规则）。
const hostPasswordAllowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789~!@#$&*()_-+=|{}[;:<>?,./"

// ValidHostPassword 校验主机密码：长度>=6、含大写+小写+数字、不以 "/" 开头。
func ValidHostPassword(s string) bool {
	if len(s) < 6 || strings.HasPrefix(s, "/") {
		return false
	}
	upper, lower, digit := false, false, false
	for _, c := range s {
		if !strings.ContainsRune(hostPasswordAllowed, c) {
			return false
		}
		switch {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	return upper && lower && digit
}

// RandomHostPassword 生成合规主机密码：长度12、必含大写+小写+数字、不以 "/" 开头。
func RandomHostPassword() string {
	for {
		var b []byte
		upper, lower, digit := false, false, false
		for i := 0; i < 12; i++ {
			var rb [1]byte
			if _, err := cryptorand.Read(rb[:]); err != nil {
				return "A1b2c3d4e5f6" // 兜底：保证合规（理论上不会失败）
			}
			c := hostPasswordChars[int(rb[0])%len(hostPasswordChars)]
			switch {
			case c >= 'A' && c <= 'Z':
				upper = true
			case c >= 'a' && c <= 'z':
				lower = true
			case c >= '0' && c <= '9':
				digit = true
			}
			b = append(b, c)
		}
		if upper && lower && digit && b[0] != '/' {
			return string(b)
		}
	}
}
