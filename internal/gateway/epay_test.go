package gateway

import (
	"net/url"
	"testing"
)

// mapToValues 原在 gateway/urlvalues.go，仅测试使用，随测试就近放置。
func mapToValues(m map[string]string) url.Values {
	q := url.Values{}
	for k, v := range m {
		q.Set(k, v)
	}
	return q
}

func TestMD5SignUsesDirectKeySuffix(t *testing.T) {
	params := mapToValues(map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "20240101123456",
		"name":         "测试商品",
		"money":        "100.00",
	})
	if got, want := md5Sign(params, "your_key_here"), "701f819a295853322e30f47f862c9ea7"; got != want {
		t.Fatalf("官方 MD5 签名不匹配: got=%s want=%s", got, want)
	}
}

func TestVerifyNotify(t *testing.T) {
	key := "testkey123"
	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "INV20260826abcdef",
		"trade_no":     "2026082612345678",
		"trade_status": "TRADE_SUCCESS",
		"money":        "12.50",
	}
	sign := md5Sign(mapToValues(params), key)
	params["sign"] = sign
	params["sign_type"] = "MD5"
	no, trade, ok := verifyEpaySign(params, key)
	if !ok || no != params["out_trade_no"] || trade != params["trade_no"] {
		t.Fatalf("合法签名校验失败: ok=%v no=%s trade=%s", ok, no, trade)
	}
	params["money"] = "0.01" // 篡改金额
	if _, _, ok := verifyEpaySign(params, key); ok {
		t.Fatal("篡改后签名应校验失败")
	}
}

// signedEpayNotify 构造一条已签名的易支付异步通知（sign/sign_type 不参与签名计算）。
func signedEpayNotify(key string, params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+2)
	for k, v := range params {
		out[k] = v
	}
	out["sign"] = md5Sign(mapToValues(out), key)
	out["sign_type"] = "MD5"
	return out
}

// TestEpayVerifyNotifyMerchantBinding 覆盖「通知必须属于本商户」：
// 仅验签无法区分通知归属（同密钥被多站点共用时），pid 不一致必须拒绝；
// 但不带 pid 的上游分支不能被误杀。
func TestEpayVerifyNotifyMerchantBinding(t *testing.T) {
	key := "testkey123"
	base := map[string]string{
		"pid":          "1001",
		"out_trade_no": "INV20260826abcdef",
		"trade_no":     "2026082612345678",
		"trade_status": "TRADE_SUCCESS",
		"money":        "12.50",
	}
	cfg := map[string]string{"key": key, "pid": "1001"}

	if _, err := (Epay{}).VerifyNotify(NotifyRequest{Params: signedEpayNotify(key, base)}, cfg); err != nil {
		t.Fatalf("本商户通知应受理: %v", err)
	}

	foreign := make(map[string]string, len(base))
	for k, v := range base {
		foreign[k] = v
	}
	foreign["pid"] = "2002"
	if _, err := (Epay{}).VerifyNotify(NotifyRequest{Params: signedEpayNotify(key, foreign)}, cfg); err == nil {
		t.Fatal("商户号不匹配的通知必须被拒绝")
	}

	noPid := make(map[string]string, len(base))
	for k, v := range base {
		if k != "pid" {
			noPid[k] = v
		}
	}
	if _, err := (Epay{}).VerifyNotify(NotifyRequest{Params: signedEpayNotify(key, noPid)}, cfg); err != nil {
		t.Fatalf("不带 pid 的通知不应被拒绝: %v", err)
	}
}

// TestEpayVerifyNotifyRejectsEmptyKey 覆盖未配置密钥的易支付网关：
// 空密钥下攻击者能按同一规则自行算出签名，必须在验签前按失败关闭。
func TestEpayVerifyNotifyRejectsEmptyKey(t *testing.T) {
	params := map[string]string{
		"out_trade_no": "INV20260826abcdef",
		"trade_no":     "2026082612345678",
		"trade_status": "TRADE_SUCCESS",
		"money":        "12.50",
	}
	for _, cfg := range []map[string]string{nil, {}, {"key": ""}, {"key": "  "}} {
		if _, err := (Epay{}).VerifyNotify(NotifyRequest{Params: signedEpayNotify("", params)}, cfg); err == nil {
			t.Fatalf("空密钥的回调必须被拒绝，cfg=%v", cfg)
		}
	}
}
