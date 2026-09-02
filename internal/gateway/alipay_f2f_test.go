package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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
