package zjmf

import (
	"strings"
	"testing"
)

func TestValidHostPassword(t *testing.T) {
	valid := []string{"Abc12345678", "A1b2c3d4e5f6", "Ab3#xY9", "Lume@2026", "aZ9~!@#$&*()_-+="}
	for _, s := range valid {
		if !validHostPassword(s) {
			t.Errorf("应通过校验: %q", s)
		}
	}
	invalid := []string{
		"123456",          // 无大写/小写
		"abcdef",          // 无大写/数字
		"ABCDEF",          // 无小写/数字
		"abc123",          // 无大写
		"A1b2",            // 过短
		"/Abc123",         // 以 / 开头
		"A1b2c3d4e5f6$%^", // 含不允许字符 $%^
		"你好Abc123",        // 非 ASCII
		"",                // 空
	}
	for _, s := range invalid {
		if validHostPassword(s) {
			t.Errorf("应被拒绝: %q", s)
		}
	}
}

func TestRandomHostPassword(t *testing.T) {
	for i := 0; i < 500; i++ {
		pw := randomHostPassword()
		if len(pw) != 12 {
			t.Fatalf("长度错误: %q", pw)
		}
		if !validHostPassword(pw) {
			t.Fatalf("生成的密码不合规: %q", pw)
		}
		if strings.HasPrefix(pw, "/") {
			t.Fatalf("生成的密码以 / 开头: %q", pw)
		}
	}
}
