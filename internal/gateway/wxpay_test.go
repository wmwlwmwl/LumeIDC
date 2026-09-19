package gateway

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 测试夹具一律使用一眼可辨的假值：与真实格式相同的占位串
// （如 AppID 的 wx + 16 位十六进制、文档里的示例证书序列号）
// 会被 GitHub 密钥扫描误报成泄露凭据，需要人工关闭告警。
const (
	wxpayTestAPIv3Key = "test-only-apiv3-key-000000000000" // 32 位，仅用于测试
	wxpayTestGCMNonce = "123456789012"                     // 12 位，与 GCM 标准 nonce 长度一致
	wxpayAuthPrefix   = "WECHATPAY2-SHA256-RSA2048 "
)

func wxpayTestConfig(t *testing.T, merchantKey *rsa.PrivateKey, wechatPub *rsa.PublicKey, apiURL string) map[string]string {
	t.Helper()
	return map[string]string{
		"api_url":       apiURL,
		"app_id":        "test-wx-appid",
		"mch_id":        "test-mch-id",
		"private_key":   privatePEM(merchantKey),
		"api_v3_key":    wxpayTestAPIv3Key,
		"cert_serial":   "test-cert-serial",
		"public_key":    publicPEM(wechatPub),
		"public_key_id": "test-public-key-id",
	}
}

// wxpaySignForTest 复现微信侧的签名，用于构造被测的应答/回调。
func wxpaySignForTest(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// wxpaySignedResponse 构造带签名请求头的应答（时间戳 + 随机串 + 签名）。
func wxpaySignedResponse(t *testing.T, key *rsa.PrivateKey, body string) (string, string, string) {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "test-nonce-0001"
	return ts, nonce, wxpaySignForTest(t, key, ts+"\n"+nonce+"\n"+body+"\n")
}

// assertWxpayRequestSignature 校验请求签名，证明生成的 Authorization 头符合 APIv3 规范。
func assertWxpayRequestSignature(t *testing.T, r *http.Request, mchPublic *rsa.PublicKey, body string) {
	t.Helper()
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, wxpayAuthPrefix) {
		t.Fatalf("Authorization 前缀错误: %q", auth)
	}
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(auth, wxpayAuthPrefix), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			fields[kv[0]] = strings.Trim(kv[1], `"`)
		}
	}
	for _, k := range []string{"mchid", "nonce_str", "signature", "timestamp", "serial_no"} {
		if fields[k] == "" {
			t.Fatalf("Authorization 缺少 %s: %q", k, auth)
		}
	}
	if got := r.Header.Get("User-Agent"); got == "" {
		t.Fatal("缺少 User-Agent（微信强制要求）")
	}
	message := r.Method + "\n" + r.URL.RequestURI() + "\n" +
		fields["timestamp"] + "\n" + fields["nonce_str"] + "\n" + body + "\n"
	digest := sha256.Sum256([]byte(message))
	sig, err := base64.StdEncoding.DecodeString(fields["signature"])
	if err != nil {
		t.Fatalf("signature 不是合法 Base64: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(mchPublic, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("请求签名验签失败: %v", err)
	}
}

// wxpayEncryptForTest 用 APIv3 密钥加密业务数据，模拟微信的 resource。
func wxpayEncryptForTest(t *testing.T, plaintext, associatedData string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(wxpayTestAPIv3Key))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, []byte(wxpayTestGCMNonce), []byte(plaintext), []byte(associatedData))
	return base64.StdEncoding.EncodeToString(sealed)
}

func TestWxpayValidateConfig(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	base := wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, "")

	if err := (Wxpay{}).ValidateConfig(base); err != nil {
		t.Fatalf("完整凭据不应报错: %v", err)
	}
	for _, key := range []string{"app_id", "mch_id", "private_key", "api_v3_key", "cert_serial", "public_key", "public_key_id"} {
		broken := map[string]string{}
		for k, v := range base {
			broken[k] = v
		}
		broken[key] = ""
		if err := (Wxpay{}).ValidateConfig(broken); err == nil {
			t.Fatalf("缺少 %s 时应校验失败", key)
		}
	}
	shortKey := map[string]string{}
	for k, v := range base {
		shortKey[k] = v
	}
	shortKey["api_v3_key"] = "tooshort"
	if err := (Wxpay{}).ValidateConfig(shortKey); err == nil {
		t.Fatal("APIv3 密钥长度不足时应校验失败")
	}
	badPEM := map[string]string{}
	for k, v := range base {
		badPEM[k] = v
	}
	badPEM["private_key"] = "not-a-key"
	if err := (Wxpay{}).ValidateConfig(badPEM); err == nil {
		t.Fatal("私钥格式非法时应校验失败")
	}
}

// 请求签名串必须为「方法\nURL\n时间戳\n随机串\n报文\n」，且末尾换行不可省略。
func TestWxpayRequestSignatureContent(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const body = `{"out_trade_no":"INV1"}`
	sig, err := wxpaySign(key, http.MethodPost, wxpayNativePath, "1554208460", "nonce-1", body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("签名不是合法 Base64: %v", err)
	}
	message := http.MethodPost + "\n" + wxpayNativePath + "\n1554208460\nnonce-1\n" + body + "\n"
	digest := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], raw); err != nil {
		t.Fatalf("请求签名不符合 APIv3 规范: %v", err)
	}
	// 末尾换行是规范的一部分，少了它微信会验签失败
	trimmed := sha256.Sum256([]byte(strings.TrimSuffix(message, "\n")))
	if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, trimmed[:], raw) == nil {
		t.Fatal("待签串缺少末尾换行时不应验签通过")
	}
}

// 应答/回调验签串为「时间戳\n随机串\n报文\n」，任一字段被改都应拒绝。
func TestWxpayVerifyRoundTrip(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const body = `{"out_trade_no":"INV1"}`
	const ts, nonce = "1554208460", "nonce-1"
	sig := wxpaySignForTest(t, key, ts+"\n"+nonce+"\n"+body+"\n")

	if err := wxpayVerify(&key.PublicKey, ts, nonce, sig, body); err != nil {
		t.Fatalf("自签自验应通过: %v", err)
	}
	if err := wxpayVerify(&key.PublicKey, ts, nonce, sig, body+" "); err == nil {
		t.Fatal("报文被篡改后应验签失败")
	}
	if err := wxpayVerify(&key.PublicKey, "1554208461", nonce, sig, body); err == nil {
		t.Fatal("时间戳被篡改后应验签失败")
	}
	if err := wxpayVerify(&key.PublicKey, ts, "nonce-2", sig, body); err == nil {
		t.Fatal("随机串被篡改后应验签失败")
	}
	if err := wxpayVerify(&key.PublicKey, "", "", "", body); err == nil {
		t.Fatal("缺少签名请求头时应报错")
	}
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	if err := wxpayVerify(&otherKey.PublicKey, ts, nonce, sig, body); err == nil {
		t.Fatal("用错误的公钥验签应失败")
	}
}

// 回调解密：只认正确的 APIv3 密钥，密钥错误必须失败（否则伪造回调可被解密）。
func TestWxpayDecryptResource(t *testing.T) {
	const plain = `{"out_trade_no":"INV1","trade_state":"SUCCESS"}`
	ciphertext := wxpayEncryptForTest(t, plain, "transaction")

	got, err := wxpayDecryptResource(wxpayTestAPIv3Key, wxpayTestGCMNonce, "transaction", ciphertext)
	if err != nil {
		t.Fatalf("正确密钥应能解密: %v", err)
	}
	if string(got) != plain {
		t.Fatalf("解密结果 = %s, 期望 %s", got, plain)
	}
	if _, err := wxpayDecryptResource("ffffffffffffffffffffffffffffffff", wxpayTestGCMNonce, "transaction", ciphertext); err == nil {
		t.Fatal("APIv3 密钥错误时应解密失败")
	}
	if _, err := wxpayDecryptResource(wxpayTestAPIv3Key, wxpayTestGCMNonce, "wrong-aad", ciphertext); err == nil {
		t.Fatal("associated_data 不匹配时应解密失败")
	}
	if _, err := wxpayDecryptResource("short", wxpayTestGCMNonce, "transaction", ciphertext); err == nil {
		t.Fatal("密钥长度非法时应报错")
	}
}

func TestWxpayNativePay(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	const codeURL = "weixin://wxpay/bizpayurl?pr=abc123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wxpayNativePath {
			t.Errorf("请求路径 = %q, 期望 %q", r.URL.Path, wxpayNativePath)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"out_trade_no":"INV20260919wx"`) {
			t.Errorf("下单请求体缺少订单号: %s", body)
		}
		if !strings.Contains(string(body), `"total":2001`) {
			t.Errorf("金额应转为分: %s", body)
		}
		assertWxpayRequestSignature(t, r, &merchantKey.PublicKey, string(body))

		resp := `{"code_url":"` + codeURL + `"}`
		ts, nonce, sig := wxpaySignedResponse(t, wechatKey, resp)
		w.Header().Set("Wechatpay-Timestamp", ts)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", sig)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)

	got, err := (Wxpay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV20260919wx", Amount: "20.01", Title: "站点 账单 INV20260919wx",
		NotifyURL: "https://shop.example.com/pay/notify/wxpay_main",
		Config:    wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, srv.URL),
	})
	if err != nil {
		t.Fatalf("Native 下单失败: %v", err)
	}
	want := LocalCheckoutPath + "?invoice=INV20260919wx&data=" + url.QueryEscape(codeURL)
	if got.URL != want {
		t.Fatalf("结算页地址 = %q, 期望 %q", got.URL, want)
	}
}

// 应答验签失败必须阻断下单：否则被篡改的 code_url 会让用户扫到攻击者的码。
func TestWxpayNativePayRejectsBadResponseSignature(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"code_url":"weixin://wxpay/bizpayurl?pr=evil"}`
		// 用错误的密钥签名，模拟应答被篡改
		ts, nonce, sig := wxpaySignedResponse(t, otherKey, resp)
		w.Header().Set("Wechatpay-Timestamp", ts)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", sig)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)

	if _, err := (Wxpay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV1", Amount: "20.01", Title: "账单",
		NotifyURL: "https://shop.example.com/pay/notify/wxpay_main",
		Config:    wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, srv.URL),
	}); err == nil {
		t.Fatal("应答验签失败时应返回错误")
	}
}

func TestWxpayH5Pay(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	const h5URL = "https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb?prepay_id=xyz"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wxpayH5Path {
			t.Errorf("请求路径 = %q, 期望 %q", r.URL.Path, wxpayH5Path)
		}
		body, _ := io.ReadAll(r.Body)
		// H5 支付必须带 payer_client_ip，否则微信会拒单
		if !strings.Contains(string(body), `"payer_client_ip":"203.0.113.7"`) {
			t.Errorf("H5 下单缺少 payer_client_ip: %s", body)
		}
		if !strings.Contains(string(body), `"type":"Wap"`) {
			t.Errorf("H5 下单缺少 h5_info.type: %s", body)
		}
		assertWxpayRequestSignature(t, r, &merchantKey.PublicKey, string(body))

		resp := `{"h5_url":"` + h5URL + `"}`
		ts, nonce, sig := wxpaySignedResponse(t, wechatKey, resp)
		w.Header().Set("Wechatpay-Timestamp", ts)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", sig)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)

	cfg := wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, srv.URL)
	returnURL := "https://shop.example.com/pay/12"
	got, err := (Wxpay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV20260919wx", Amount: "20.01", Title: "账单",
		NotifyURL: "https://shop.example.com/pay/notify/wxpay_main",
		ReturnURL: returnURL, ClientIP: "203.0.113.7", IsMobile: true,
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("H5 下单失败: %v", err)
	}
	want := h5URL + "&redirect_url=" + url.QueryEscape(returnURL)
	if got.URL != want {
		t.Fatalf("H5 支付链接 = %q, 期望 %q", got.URL, want)
	}

	// 缺少用户 IP 时必须提前失败，而不是把请求发出去让微信拒单
	if _, err := (Wxpay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV1", Amount: "20.01", Title: "账单",
		NotifyURL: "https://shop.example.com/pay/notify/wxpay_main",
		IsMobile:  true, Config: cfg,
	}); err == nil {
		t.Fatal("H5 支付缺少 ClientIP 时应返回错误")
	}
}

func TestWxpayVerifyNotify(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	cfg := wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, "")

	const aad = "transaction"
	plain := `{"out_trade_no":"INV20260919wx","transaction_id":"4200001234202609190001",` +
		`"trade_state":"SUCCESS","amount":{"total":2001,"payer_total":2001,"currency":"CNY"}}`
	body := `{"id":"EV-2026091900001","event_type":"TRANSACTION.SUCCESS","resource_type":"encrypt-resource",` +
		`"resource":{"algorithm":"AEAD_AES_256_GCM","ciphertext":"` + wxpayEncryptForTest(t, plain, aad) +
		`","nonce":"` + wxpayTestGCMNonce + `","associated_data":"` + aad + `"},"summary":"支付成功"}`

	ts, nonce, sig := wxpaySignedResponse(t, wechatKey, body)
	headers := map[string]string{
		"wechatpay-timestamp": ts, "wechatpay-nonce": nonce, "wechatpay-signature": sig,
	}
	got, err := (Wxpay{}).VerifyNotify(NotifyRequest{RawBody: body, Headers: headers}, cfg)
	if err != nil {
		t.Fatalf("合法回调应校验通过: %v", err)
	}
	if !got.Successful || got.InvoiceNo != "INV20260919wx" ||
		got.TradeNo != "4200001234202609190001" || got.Amount != "20.01" {
		t.Fatalf("回调解析结果不符: %+v", got)
	}

	// 签名被篡改
	badHeaders := map[string]string{
		"wechatpay-timestamp": ts, "wechatpay-nonce": nonce,
		"wechatpay-signature": sig[:len(sig)-4] + "AAAA",
	}
	if _, err := (Wxpay{}).VerifyNotify(NotifyRequest{RawBody: body, Headers: badHeaders}, cfg); err == nil {
		t.Fatal("签名被篡改时应校验失败")
	}
	// 报文被篡改
	if _, err := (Wxpay{}).VerifyNotify(NotifyRequest{RawBody: body + " ", Headers: headers}, cfg); err == nil {
		t.Fatal("报文被篡改时应校验失败")
	}
	// 缺少报文
	if _, err := (Wxpay{}).VerifyNotify(NotifyRequest{Headers: headers}, cfg); err == nil {
		t.Fatal("缺少报文时应报错")
	}
}

// 非支付成功事件（如退款通知）验签通过后应受理但不核销账单。
func TestWxpayVerifyNotifyIgnoresOtherEvents(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	cfg := wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, "")

	body := `{"id":"EV-2","event_type":"REFUND.SUCCESS","resource":{"algorithm":"AEAD_AES_256_GCM",` +
		`"ciphertext":"` + wxpayEncryptForTest(t, `{}`, "") + `","nonce":"` + wxpayTestGCMNonce + `","associated_data":""}}`
	ts, nonce, sig := wxpaySignedResponse(t, wechatKey, body)
	got, err := (Wxpay{}).VerifyNotify(NotifyRequest{RawBody: body, Headers: map[string]string{
		"wechatpay-timestamp": ts, "wechatpay-nonce": nonce, "wechatpay-signature": sig,
	}}, cfg)
	if err != nil {
		t.Fatalf("退款通知验签应通过: %v", err)
	}
	if got.Successful {
		t.Fatal("非支付成功事件不应标记为已支付")
	}
}

// 微信对回调地址与应答体有硬性要求：地址不能带查询参数、应答必须是 JSON。
func TestWxpayNotifyContract(t *testing.T) {
	if got, want := (Wxpay{}).NotifyAck(), `{"code":"SUCCESS","message":"成功"}`; got != want {
		t.Fatalf("NotifyAck = %q, 期望 %q", got, want)
	}
	got := (Wxpay{}).NotifyURL("https://shop.example.com/", "wxpay_main")
	if got != "https://shop.example.com/pay/wxpay_main/notify" {
		t.Fatalf("NotifyURL = %q", got)
	}
	if strings.Contains(got, "?") {
		t.Fatalf("回调地址不得携带查询参数: %q", got)
	}
	if got, want := (Wxpay{}).CheckoutPath(), LocalCheckoutPath; got != want {
		t.Fatalf("CheckoutPath = %q, 期望 %q", got, want)
	}
	if _, ok := any(Wxpay{}).(LocalCheckout); !ok {
		t.Fatal("微信 Native 支付依赖本地二维码结算页")
	}
	if _, ok := any(Wxpay{}).(OrderQuerier); !ok {
		t.Fatal("微信支付应实现 OrderQuerier，补单依赖它")
	}
	if _, ok := any(Wxpay{}).(ConfigValidator); !ok {
		t.Fatal("微信支付应实现 ConfigValidator")
	}
}

// 微信内置浏览器无法使用 H5 支付，必须给出可执行的引导而不是笼统报错。
func TestWxpayRejectsWeChatBrowser(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, err := (Wxpay{}).PayURL(context.Background(), PayRequest{
		InvoiceNo: "INV1", Amount: "20.01", Title: "账单",
		NotifyURL: "https://shop.example.com/pay/notify/wxpay_main",
		IsMobile:  true, IsWeChatBrowser: true, ClientIP: "203.0.113.7",
		Config: wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, "https://api.mch.weixin.qq.com"),
	})
	if err == nil {
		t.Fatal("微信内置浏览器应被拦截")
	}
	var uf UserFacingError
	if !errors.As(err, &uf) {
		t.Fatalf("应返回可展示给用户的提示，实际: %v", err)
	}
	if !strings.Contains(uf.Msg, "浏览器") {
		t.Fatalf("提示应引导用户改用浏览器: %q", uf.Msg)
	}
}

func TestWxpayQueryOrder(t *testing.T) {
	merchantKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wechatKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, wxpayQueryPath) {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "mchid=test-mch-id") {
			t.Errorf("查单缺少 mchid: %q", r.URL.RawQuery)
		}
		assertWxpayRequestSignature(t, r, &merchantKey.PublicKey, "")

		resp := `{"transaction_id":"4200001234202609190002","out_trade_no":"INV20260919wx",` +
			`"trade_state":"SUCCESS","amount":{"total":2001}}`
		ts, nonce, sig := wxpaySignedResponse(t, wechatKey, resp)
		w.Header().Set("Wechatpay-Timestamp", ts)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", sig)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)

	got, err := (Wxpay{}).QueryOrder(context.Background(), QueryOrderRequest{
		InvoiceNo: "INV20260919wx",
		Config:    wxpayTestConfig(t, merchantKey, &wechatKey.PublicKey, srv.URL),
	})
	if err != nil {
		t.Fatalf("查单失败: %v", err)
	}
	if !got.Paid || got.Amount != "20.01" || got.TradeNo != "4200001234202609190002" {
		t.Fatalf("查单结果不符: %+v", got)
	}
}
