package service

import (
	"testing"

	// 实名渠道适配器已迁 plugins/verify（接口类扩展轨道），URL 白名单校验依赖其描述符注册。
	_ "lumeidc/internal/plugins/verify"
)

// checkPluginReturnURL：认证页地址必须 https + 无用户态 + host 命中渠道描述符白名单（fail-closed）。
func TestCheckPluginReturnURL(t *testing.T) {
	okCases := []struct{ provider, url string }{
		{"baidu_face", "https://brain.baidu.com/face/print/?token=abc"},
		{"leaf_face", "https://face.ly-y.cn/verify?task=1"},
		{"smapi", "https://smapi.x1m1.cn/certify"},
		{"stay33", "https://idc.stay33.cn/realname/page"},
		{"baidu_face", ""}, // 空 URL 放行（无需跳转的渠道）
	}
	for _, tc := range okCases {
		if err := checkPluginReturnURL(tc.provider, tc.url); err != nil {
			t.Errorf("%s %q 应放行: %v", tc.provider, tc.url, err)
		}
	}
	badCases := []struct{ provider, url string }{
		{"baidu_face", "http://brain.baidu.com/x"},               // 非 https
		{"baidu_face", "https://u:p@brain.baidu.com/x"},          // 带用户态
		{"baidu_face", "https://evil.com/face/print"},            // host 不在白名单
		{"baidu_face", "https://brain.baidu.com.evil.com/"},      // 后缀仿冒
		{"no_such", "https://brain.baidu.com/face/print"},        // 未注册渠道 fail-closed
		{"baidu_face", "https://brain.baidu.com/x\r\nInject: 1"}, // CRLF 注入
	}
	for _, tc := range badCases {
		if err := checkPluginReturnURL(tc.provider, tc.url); err == nil {
			t.Errorf("%s %q 应被拒绝", tc.provider, tc.url)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		// 大陆号：无前缀、86 前缀、+86 前缀均归一为 +86 E.164
		"138 0013 8000":  "+8613800138000",
		"+8613800138000": "+8613800138000",
		"8613800138000":  "+8613800138000",
		// 国际号：按 E.164 原样返回（含 +86…，与历史行为等价）
		"+85261234567":    "+85261234567",
		"+852-6123-4567":  "+85261234567",
		"+14155552671":    "+14155552671",
		"+85213800138000": "+85213800138000",
	}
	for input, want := range cases {
		got, err := NormalizePhone(input)
		if err != nil || got != want {
			t.Fatalf("NormalizePhone(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "12345678901", "+0123", "+123", "+1a2345678", "abc"} {
		if _, err := NormalizePhone(input); err == nil {
			t.Fatalf("NormalizePhone(%q) unexpectedly succeeded", input)
		}
	}
}

func TestNormalizeIdentityNumber(t *testing.T) {
	want := "11010519491231002X"
	for _, input := range []string{want, "110105491231002"} {
		got, err := normalizeIdentityNumber(input)
		if err != nil || got != want {
			t.Fatalf("normalizeIdentityNumber(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeIdentityNumber("110105194912310021"); err == nil {
		t.Fatal("invalid checksum unexpectedly succeeded")
	}
}

func TestIdentityMasks(t *testing.T) {
	if got := MaskPhone("+8613800138000"); got != "+86138****8000" {
		t.Fatalf("MaskPhone() = %q", got)
	}
	if got := MaskIdentityNumber("110101199001011234"); got != "1101**********1234" {
		t.Fatalf("MaskIdentityNumber() = %q", got)
	}
	if got := StatusText("pending"); got != "待人工审核" {
		t.Fatalf("StatusText() = %q", got)
	}
}

func TestValidLegalName(t *testing.T) {
	for _, name := range []string{"张三", "欧阳娜娜"} {
		if !validLegalName(name) {
			t.Fatalf("validLegalName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "A1", "张\n三"} {
		if validLegalName(name) {
			t.Fatalf("validLegalName(%q) = true", name)
		}
	}
}
