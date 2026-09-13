package gateway

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
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

	got, err := (AlipayF2F{}).QueryOrder(context.Background(), QueryOrderRequest{
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

	got, err := (AlipayF2F{}).QueryOrder(context.Background(), QueryOrderRequest{
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

	_, err := (AlipayF2F{}).QueryOrder(context.Background(), QueryOrderRequest{
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
