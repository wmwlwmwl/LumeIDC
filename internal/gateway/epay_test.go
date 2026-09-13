package gateway

import "testing"

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
