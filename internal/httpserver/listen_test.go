package httpserver

import "testing"

// TestSameListen 同址幂等比较：后台留空回退 config 端口时不得"自己绑自己"。
func TestSameListen(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{":8080", ":8080", true},               // 留空回退与当前一致
		{":8080", "0.0.0.0:8080", true},        // 全接口等价
		{":8080", "[::]:8080", true},           // IPv6 全接口等价
		{"127.0.0.1:8080", "127.0.0.1:8080", true},
		{"127.0.0.1:8080", ":8080", false},     // 明确主机与全接口不同址
		{":8080", ":9090", false},              // 不同端口
		{"8080", ":8080", false},               // 非法地址（无冒号）
		{"", ":8080", false},                   // 空当前地址 => 需真实绑定
	}
	for _, c := range cases {
		if got := sameListen(c.a, c.b); got != c.want {
			t.Errorf("sameListen(%q, %q) = %v，期望 %v", c.a, c.b, got, c.want)
		}
	}
}