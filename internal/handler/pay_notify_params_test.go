package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotifyParamsExcludesCode(t *testing.T) {
	// 回调 URL 带本站路由参数 code，body 为网关真实参数；code 不得参与验签。
	r := httptest.NewRequest("POST", "/pay/notify?code=alipay_f2f",
		strings.NewReader("out_trade_no=INV1&trade_no=T1&sign=abc"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	params := notifyParams(r)
	if _, ok := params["code"]; ok {
		t.Fatal("URL 查询参数 code 不应进入回调验签参数")
	}
	if params["out_trade_no"] != "INV1" || params["trade_no"] != "T1" || params["sign"] != "abc" {
		t.Fatalf("回调 body 参数缺失: %v", params)
	}
}
