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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AlipayF2F implements Alipay face-to-face precreate payment (RSA2).
type AlipayF2F struct{}

func (AlipayF2F) Driver() string { return "alipay_f2f" }
func (AlipayF2F) Name() string   { return "支付宝当面付" }

func (AlipayF2F) PayURL(ctx context.Context, req PayRequest) (string, error) {
	appID := strings.TrimSpace(req.Config["app_id"])
	privateKey := strings.TrimSpace(req.Config["private_key"])
	if appID == "" || privateKey == "" {
		return "", fmt.Errorf("支付宝当面付未配置完整（需要 app_id/private_key）")
	}
	endpoint := strings.TrimRight(req.Config["api_url"], "/")
	if endpoint == "" {
		endpoint = "https://openapi.alipay.com/gateway.do"
	}
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("支付宝商户私钥无效: %w", err)
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": req.InvoiceNo, "total_amount": req.Amount, "subject": req.Title})
	params := url.Values{
		"app_id": {appID}, "method": {"alipay.trade.precreate"}, "format": {"json"},
		"charset": {"UTF-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")},
		"version": {"1.0"}, "notify_url": {req.NotifyURL}, "biz_content": {string(biz)},
	}
	sign, err := signAlipay(params, key)
	if err != nil {
		return "", fmt.Errorf("支付宝请求签名失败: %w", err)
	}
	params.Set("sign", sign)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("请求支付宝失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("支付宝接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取支付宝响应失败: %w", err)
	}
	var result struct {
		Response struct {
			Code   string `json:"code"`
			Msg    string `json:"msg"`
			SubMsg string `json:"sub_msg"`
			QRCode string `json:"qr_code"`
		} `json:"alipay_trade_precreate_response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("支付宝响应格式无效")
	}
	if result.Response.Code != "10000" || result.Response.QRCode == "" {
		return "", fmt.Errorf("支付宝创建二维码失败: %s %s", result.Response.Msg, result.Response.SubMsg)
	}
	return "/pay/alipay-f2f?invoice=" + url.QueryEscape(req.InvoiceNo) + "&qr=" + url.QueryEscape(result.Response.QRCode), nil
}

func (AlipayF2F) VerifyNotify(params map[string]string, cfg map[string]string) (NotifyResult, error) {
	signature := strings.TrimSpace(params["sign"])
	if signature == "" || params["out_trade_no"] == "" || params["trade_no"] == "" {
		return NotifyResult{}, fmt.Errorf("支付宝回调缺少必要字段")
	}
	key, err := parsePublicKey(cfg["public_key"])
	if err != nil {
		return NotifyResult{}, fmt.Errorf("支付宝公钥无效: %w", err)
	}
	if err := verifyAlipay(params, signature, key); err != nil {
		return NotifyResult{}, fmt.Errorf("支付宝回调签名校验失败")
	}
	if params["trade_status"] != "TRADE_SUCCESS" && params["trade_status"] != "TRADE_FINISHED" {
		return NotifyResult{InvoiceNo: params["out_trade_no"], TradeNo: params["trade_no"]}, nil
	}
	if params["receipt_amount"] == "" && params["total_amount"] == "" {
		return NotifyResult{}, fmt.Errorf("支付宝回调缺少金额")
	}
	// total_amount is the signed order amount; receipt_amount may reflect a partial
	// settlement and must not be used to bypass the invoice amount check.
	amount := params["total_amount"]
	if amount == "" {
		amount = params["receipt_amount"]
	}
	return NotifyResult{InvoiceNo: params["out_trade_no"], TradeNo: params["trade_no"], Amount: amount, Successful: true}, nil
}

func alipaySignText(params url.Values) string {
	keys := make([]string, 0, len(params))
	for key, values := range params {
		if key != "sign" && len(values) > 0 && values[0] != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = key + "=" + params.Get(key)
	}
	return strings.Join(parts, "&")
}

func signAlipay(params url.Values, key *rsa.PrivateKey) (string, error) {
	digest := sha256.Sum256([]byte(alipaySignText(params)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return base64.StdEncoding.EncodeToString(signature), err
}

func verifyAlipay(params map[string]string, signature string, key *rsa.PublicKey) error {
	values := url.Values{}
	for name, value := range params {
		values.Set(name, value)
	}
	values.Del("sign")
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(alipaySignText(values)))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], raw)
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
		if err != nil {
			return nil, fmt.Errorf("不是有效的 PEM 或 Base64 私钥")
		}
		block = &pem.Block{Bytes: decoded}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("不是 RSA 私钥")
	}
	return rsaKey, nil
}

func parsePublicKey(raw string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
		if err != nil {
			return nil, fmt.Errorf("不是有效的 PEM 或 Base64 公钥")
		}
		block = &pem.Block{Bytes: decoded}
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}
