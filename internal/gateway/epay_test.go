package gateway

import "testing"

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
	no, trade, ok := VerifyNotify(params, key)
	if !ok || no != params["out_trade_no"] || trade != params["trade_no"] {
		t.Fatalf("合法签名校验失败: ok=%v no=%s trade=%s", ok, no, trade)
	}
	params["money"] = "0.01" // 篡改金额
	if _, _, ok := VerifyNotify(params, key); ok {
		t.Fatal("篡改后签名应校验失败")
	}
}
