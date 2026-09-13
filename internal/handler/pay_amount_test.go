package handler

import "testing"

func TestEqualAmount(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.00", "10", true},
		{"10.10", "10.1", true},
		{"0.01", "0.01", true},
		{"10.00", "10.01", false},
		{"0.00", "0.00", false},  // 0 元不是有效支付金额
		{"", "0.00", false},      // 空值拒绝
		{"abc", "10.00", false},  // 非金额拒绝
		{"-1.00", "1.00", false}, // 负数拒绝
		{"1.001", "1.00", false}, // 超两位小数拒绝
	}
	for _, c := range cases {
		if got := equalAmount(c.a, c.b); got != c.want {
			t.Errorf("equalAmount(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
