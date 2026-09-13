package server

import (
	cryptorand "crypto/rand"
	mathrand "math/rand"
	"strings"
)

// 主机密码允许字符集（大写+小写+数字+常见特殊符号），各上游通用。
const hostPasswordChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789~!@#$&*()_-+="
const hostAlnumChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

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

// randCharset 从字符集随机取 n 个字符；拒绝采样消除取模偏差
// （256 不整除字符集长度时，首部字符出现概率偏高）。
// ponytail: crypto/rand 读取失败时整段回退自动播种的 math/rand——仅此路径随机度下降，
// 用于规避旧实现"熵源失败返回固定密码"的隐患；熵源失败通常伴随系统级故障。
func randCharset(chars string, n int) string {
	// 用 int 承载 max：字符集长度整除 256 时 max=256，byte 会溢出为 0 导致采样死循环
	max := 256 - (256 % len(chars))
	out := make([]byte, 0, n)
	for len(out) < n {
		var rb [1]byte
		if _, err := cryptorand.Read(rb[:]); err != nil {
			out = make([]byte, n)
			for i := range out {
				out[i] = chars[mathrand.Intn(len(chars))]
			}
			return string(out)
		}
		if int(rb[0]) >= max {
			continue
		}
		out = append(out, chars[int(rb[0])%len(chars)])
	}
	return string(out)
}

// RandomHostPassword 生成合规主机密码：长度12、必含大写+小写+数字、不以 "/" 开头。
func RandomHostPassword() string {
	for i := 0; i < 64; i++ {
		s := randCharset(hostPasswordChars, 12)
		if ValidHostPassword(s) {
			return s
		}
	}
	// 保底：显式构造合规前缀（已含大写+小写+数字）+ 随机尾部，任何情况下都不是固定密码
	return "A1b2" + randCharset(hostPasswordChars, 8)
}

// RandomAlnum 随机字母数字串（主机名补位等非密码场景，不含特殊字符）。
func RandomAlnum(n int) string {
	return randCharset(hostAlnumChars, n)
}
