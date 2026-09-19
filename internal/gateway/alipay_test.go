package gateway

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAlipaySignatureRoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟支付宝服务端按 rsaCheckV1 规则对通知签名：签名内容剔除 sign_type
	params := url.Values{"z": {"last"}, "a": {"first"}, "empty": {""}}
	signature, err := signAlipay(params, key)
	if err != nil {
		t.Fatal(err)
	}
	params.Set("sign", signature)
	params.Set("sign_type", "RSA2")
	values := map[string]string{}
	for name, value := range params {
		values[name] = value[0]
	}
	publicKey, err := parsePublicKey(publicPEM(&key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAlipay(values, signature, publicKey); err != nil {
		t.Fatalf("round trip verification failed: %v", err)
	}
	values["a"] = "tampered"
	if err := verifyAlipay(values, signature, publicKey); err == nil {
		t.Fatal("tampered notification was accepted")
	}
}

// TestAlipayVerifyNotifyMerchantBinding 覆盖「通知必须来自本应用」：
// app_id 不一致必须拒绝；字段缺失的老通知不能被误杀。
// 不校验 seller_id——公钥按 APPID 签发、验签已绑定归属，而它是「只用于校验
// 不参与下单」的可选字段，填错会让下单照常成功、回调被静默拒掉。
func TestAlipayVerifyNotifyMerchantBinding(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]string{
		"app_id":     "2021000000000001",
		"public_key": publicPEM(&key.PublicKey),
		// 老部署的 config 里可能残留 seller_id（曾短暂支持过）：必须被忽略，
		// 否则历史配置配错过的站点会重新开始静默拒收回调。
		"seller_id": "2088000000000001",
	}
	// notify 按支付宝 rsaCheckV1 规则签名：签名内容剔除 sign 与 sign_type；
	// extra 里值为空的键不写入参数（模拟老版本通知不带该字段）。
	notify := func(extra map[string]string) NotifyRequest {
		params := map[string]string{
			"out_trade_no": "INV20260826abcdef",
			"trade_no":     "2026082612345678",
			"trade_status": "TRADE_SUCCESS",
			"total_amount": "12.50",
		}
		for k, v := range extra {
			if v != "" {
				params[k] = v
			}
		}
		signature, serr := signAlipay(mapToValues(params), key)
		if serr != nil {
			t.Fatal(serr)
		}
		params["sign"] = signature
		params["sign_type"] = "RSA2"
		return NotifyRequest{Params: params}
	}

	cases := []struct {
		name    string
		appID   string
		wantErr bool
	}{
		{name: "本应用", appID: cfg["app_id"]},
		{name: "app_id 缺失不误杀"},
		{name: "应用 ID 不匹配", appID: "9999999999999999", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, verr := (Alipay{}).VerifyNotify(notify(map[string]string{
				"app_id": tc.appID,
				// 通知里带别的收款账号也不该影响结果（不再比对 seller_id）。
				"seller_id": "2088999999999999",
			}), cfg)
			if tc.wantErr {
				if verr == nil {
					t.Fatalf("应被拒绝，实际受理: %+v", result)
				}
				return
			}
			if verr != nil || !result.Successful {
				t.Fatalf("应受理: err=%v result=%+v", verr, result)
			}
		})
	}
}

func publicPEM(key *rsa.PublicKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(key)}))
}

func privatePEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// signedAlipayBody 模拟支付宝对业务响应节点签名后返回的响应体。
func signedAlipayBody(t *testing.T, key *rsa.PrivateKey, node string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(node))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return `{"alipay_trade_query_response":` + node + `,"sign":"` + base64.StdEncoding.EncodeToString(signature) + `"}`
}

func alipayQueryServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析查询请求失败: %v", err)
		}
		if got := r.PostFormValue("method"); got != "alipay.trade.query" {
			t.Errorf("method = %q, want alipay.trade.query", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAlipayQueryOrder(t *testing.T) {
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	node := `{"code":"10000","msg":"Success","trade_no":"2026091200001","out_trade_no":"INV20260912abc","trade_status":"TRADE_SUCCESS","total_amount":"3.10"}`
	srv := alipayQueryServer(t, signedAlipayBody(t, serverKey, node))

	got, err := (Alipay{}).QueryOrder(context.Background(), QueryOrderRequest{
		InvoiceNo: "INV20260912abc",
		Config: map[string]string{
			"app_id":      "2021000000000000",
			"private_key": privatePEM(merchantKey),
			"public_key":  publicPEM(&serverKey.PublicKey),
			"api_url":     srv.URL,
		},
	})
	if err != nil {
		t.Fatalf("查询订单失败: %v", err)
	}
	if !got.Paid || got.TradeNo != "2026091200001" || got.OutTradeNo != "INV20260912abc" || got.Amount != "3.10" {
		t.Fatalf("查询结果不符: %+v", got)
	}
}

func TestAlipayQueryOrderUnpaid(t *testing.T) {
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	node := `{"code":"10000","msg":"Success","trade_no":"2026091200002","out_trade_no":"INV20260912xyz","trade_status":"WAIT_BUYER_PAY","total_amount":"3.10"}`
	srv := alipayQueryServer(t, signedAlipayBody(t, serverKey, node))

	got, err := (Alipay{}).QueryOrder(context.Background(), QueryOrderRequest{
		InvoiceNo: "INV20260912xyz",
		Config: map[string]string{
			"app_id":      "2021000000000000",
			"private_key": privatePEM(merchantKey),
			"public_key":  publicPEM(&serverKey.PublicKey),
			"api_url":     srv.URL,
		},
	})
	if err != nil {
		t.Fatalf("查询订单失败: %v", err)
	}
	if got.Paid {
		t.Fatalf("未支付订单不应标记为已支付: %+v", got)
	}
}

func TestAlipayQueryOrderRejectsBadSignature(t *testing.T) {
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	node := `{"code":"10000","msg":"Success","trade_no":"2026091200003","out_trade_no":"INV20260912bad","trade_status":"TRADE_SUCCESS","total_amount":"3.10"}`
	srv := alipayQueryServer(t, signedAlipayBody(t, serverKey, node))

	_, err := (Alipay{}).QueryOrder(context.Background(), QueryOrderRequest{
		InvoiceNo: "INV20260912bad",
		Config: map[string]string{
			"app_id":      "2021000000000000",
			"private_key": privatePEM(merchantKey),
			"public_key":  publicPEM(&otherKey.PublicKey), // 与签名密钥不匹配
			"api_url":     srv.URL,
		},
	})
	if err == nil {
		t.Fatal("签名不匹配时应返回错误")
	}
}

// 未配置 payment_mode 时必须沿用当面付预下单（本地二维码结算页），
// 防止新增跳转模式时改变默认行为。
func TestAlipayPayURLDefaultsToPrecreate(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析预下单请求失败: %v", err)
		}
		if got := r.PostFormValue("method"); got != "alipay.trade.precreate" {
			t.Errorf("method = %q, 期望 alipay.trade.precreate", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"alipay_trade_precreate_response":{"code":"10000","msg":"Success","out_trade_no":"INV20260919qr","qr_code":"https://qr.alipay.com/bax0"}}`)
	}))
	t.Cleanup(srv.Close)

	got, err := (Alipay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV20260919qr", Amount: "20.01", Title: "站点 账单 INV20260919qr",
		NotifyURL: "https://shop.example.com/pay/notify?code=zfb",
		Config: map[string]string{
			"app_id": "2021000000000000", "private_key": privatePEM(merchantKey), "api_url": srv.URL,
		},
	})
	if err != nil {
		t.Fatalf("当面付预下单失败: %v", err)
	}
	want := LocalCheckoutPath + "?invoice=INV20260919qr&data=" + url.QueryEscape("https://qr.alipay.com/bax0")
	if got.URL != want {
		t.Fatalf("本地结算页地址 = %q, 期望 %q", got.URL, want)
	}
}

// 跳转模式本地签名拼装支付宝收银台 URL：电脑 page.pay、手机 wap.pay，
// 且签名必须能按支付宝 rsaCheckV1 规则验签通过（否则收银台会报签名错误）。
func TestAlipayRedirectPayURL(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := parsePublicKey(publicPEM(&merchantKey.PublicKey))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		mobile      bool
		wantMethod  string
		wantProduct string
	}{
		{"电脑端走电脑网站支付", false, "alipay.trade.page.pay", "FAST_INSTANT_TRADE_PAY"},
		{"手机端走手机网站支付", true, "alipay.trade.wap.pay", "QUICK_WAP_WAY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := (Alipay{}).PayURL(context.Background(), PayRequest{
				InvoiceNo: "INV20260919abc", Amount: "20.01", Title: "站点 账单 INV20260919abc",
				NotifyURL: "https://shop.example.com/pay/notify?code=zfb",
				ReturnURL: "https://shop.example.com/pay/12",
				IsMobile:  c.mobile,
				Config: map[string]string{
					"app_id": "2021000000000000", "private_key": privatePEM(merchantKey),
					"api_url": "https://openapi.alipay.com/gateway.do", "payment_mode": "redirect",
				},
			})
			if err != nil {
				t.Fatalf("生成跳转链接失败: %v", err)
			}
			u, err := url.Parse(got.URL)
			if err != nil {
				t.Fatalf("返回地址不是合法 URL: %v", err)
			}
			if endpoint := u.Scheme + "://" + u.Host + u.Path; endpoint != "https://openapi.alipay.com/gateway.do" {
				t.Fatalf("跳转地址 = %q, 期望网关地址", endpoint)
			}
			q := u.Query()
			if q.Get("method") != c.wantMethod {
				t.Fatalf("method = %q, 期望 %q", q.Get("method"), c.wantMethod)
			}
			if q.Get("return_url") != "https://shop.example.com/pay/12" {
				t.Fatalf("return_url = %q, 缺失或错误", q.Get("return_url"))
			}
			if q.Get("notify_url") != "https://shop.example.com/pay/notify?code=zfb" {
				t.Fatalf("notify_url = %q, 缺失或错误", q.Get("notify_url"))
			}
			var biz map[string]string
			if err := json.Unmarshal([]byte(q.Get("biz_content")), &biz); err != nil {
				t.Fatalf("biz_content 不是合法 JSON: %v", err)
			}
			if biz["product_code"] != c.wantProduct {
				t.Fatalf("product_code = %q, 期望 %q", biz["product_code"], c.wantProduct)
			}
			if biz["out_trade_no"] != "INV20260919abc" || biz["total_amount"] != "20.01" {
				t.Fatalf("biz_content 订单信息错误: %+v", biz)
			}
			// 按支付宝对「请求」的加签规则独立复核：剔除 sign、剔除空值、按 key 排序拼接后
			// SHA256withRSA 验签；sign_type 属公共参数，需参与签名。
			// 注意这与通知验签（rsaCheckV1）不同——后者还会剔除 sign_type，故不能复用 verifyAlipay。
			values := url.Values{}
			for k := range q {
				values.Set(k, q.Get(k))
			}
			values.Del("sign")
			digest := sha256.Sum256([]byte(alipaySignText(values)))
			signature, err := base64.StdEncoding.DecodeString(q.Get("sign"))
			if err != nil {
				t.Fatalf("sign 不是合法 Base64: %v", err)
			}
			if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
				t.Fatalf("跳转 URL 签名验签失败: %v", err)
			}
		})
	}
}

// 扫码模式下手机端默认改走手机网站支付，避免用户在自己手机上看到无法扫描的二维码。
func TestAlipayPayURLMobileAutoRedirect(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Alipay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV20260919m1", Amount: "20.01", Title: "站点 账单 INV20260919m1",
		NotifyURL: "https://shop.example.com/pay/notify?code=zfb",
		ReturnURL: "https://shop.example.com/pay/12",
		IsMobile:  true,
		Config: map[string]string{
			// 不设 payment_mode，即扫码模式
			"app_id": "2021000000000000", "private_key": privatePEM(merchantKey),
			"api_url": "https://openapi.alipay.com/gateway.do",
		},
	})
	if err != nil {
		t.Fatalf("生成支付地址失败: %v", err)
	}
	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatalf("返回地址不是合法 URL: %v", err)
	}
	if u.Path == LocalCheckoutPath {
		t.Fatalf("扫码模式下手机端不应返回本地二维码页: %s", got.URL)
	}
	if method := u.Query().Get("method"); method != "alipay.trade.wap.pay" {
		t.Fatalf("method = %q, 期望 alipay.trade.wap.pay", method)
	}
}

// 置 mobile_qrcode=1 时手机端强制用二维码，供未签约「手机网站支付」的商户回退。
func TestAlipayPayURLMobileForceQRCode(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	qrText := "https://qr.alipay.com/bax1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析预下单请求失败: %v", err)
		}
		if got := r.PostFormValue("method"); got != "alipay.trade.precreate" {
			t.Errorf("method = %q, 期望 alipay.trade.precreate", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"alipay_trade_precreate_response":{"code":"10000","msg":"Success","out_trade_no":"INV20260919m2","qr_code":"`+qrText+`"}}`)
	}))
	t.Cleanup(srv.Close)

	got, err := (Alipay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV20260919m2", Amount: "20.01", Title: "站点 账单 INV20260919m2",
		NotifyURL: "https://shop.example.com/pay/notify?code=zfb",
		IsMobile:  true,
		Config: map[string]string{
			"app_id": "2021000000000000", "private_key": privatePEM(merchantKey),
			"api_url": srv.URL, "mobile_qrcode": "1",
		},
	})
	if err != nil {
		t.Fatalf("手机端强制扫码时应走当面付预下单: %v", err)
	}
	want := LocalCheckoutPath + "?invoice=INV20260919m2&data=" + url.QueryEscape(qrText)
	if got.URL != want {
		t.Fatalf("本地结算页地址 = %q, 期望 %q", got.URL, want)
	}
}
