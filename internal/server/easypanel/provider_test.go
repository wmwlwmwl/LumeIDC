package easypanel

import (
	"testing"
)

func TestSiteName(t *testing.T) {
	if got := SiteName(25); got != "u25" {
		t.Fatalf("SiteName(25)=%q want u25", got)
	}
	if got := SiteName(1); got != "u1" {
		t.Fatalf("SiteName(1)=%q", got)
	}
}

func TestServiceIDFromHost(t *testing.T) {
	if id, err := serviceIDFromHost(25); err != nil || id != 25 {
		t.Fatalf("serviceIDFromHost(25)=%d,%v", id, err)
	}
	for _, bad := range []int64{0, -1} {
		if _, err := serviceIDFromHost(bad); err == nil {
			t.Fatalf("serviceIDFromHost(%d) 应报错", bad)
		}
	}
}

// 签名算法验证：s = md5(a + skey + r)。
// 顺序已用真实面板实测（a=info + skey + r 返回 200）；
// 文档示例 URL 中的 s 值对应另一把 skey，其内联向量有误，此处用标准 md5 值。
func TestSignatureVector(t *testing.T) {
	got := md5hex("add", "test", "888")
	want := "d0af29175f7870817aa0168a20bdccd1" // = md5("addtest888")
	if got != want {
		t.Fatalf("签名不符: got %s want %s", got, want)
	}
	if got := md5hex("info", "k", "1"); got != md5hex("info", "k", "1") {
		t.Fatal("签名应确定性")
	}
}

func TestStrField(t *testing.T) {
	m := map[string]any{
		"s":     "abc",
		"n":     float64(42),
		"f":     float64(3.5),
		"b":     true,
		"nil":   nil,
		"numst": "123",
	}
	cases := map[string]string{"s": "abc", "n": "42", "f": "3.5", "b": "1", "nil": "", "numst": "123"}
	for k, want := range cases {
		if got := strField(m, k); got != want {
			t.Errorf("strField(%s)=%q want %q", k, got, want)
		}
	}
	if numField(m, "n") != 42 || numField(m, "numst") != 123 || numField(m, "nil") != 0 {
		t.Errorf("numField 异常")
	}
}

func TestAPICode(t *testing.T) {
	if apiCode(nil) != 0 {
		t.Fatal("nil 错误码应为 0")
	}
	if apiCode(&errAPI{code: 500}) != 500 {
		t.Fatal("errAPI 码提取失败")
	}
	if apiCode(errString("x")) != 0 {
		t.Fatal("普通错误码应为 0")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
