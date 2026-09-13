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

// CheckoutPath 声明当面付使用本站的本地二维码结算页。
func (AlipayF2F) CheckoutPath() string { return LocalCheckoutPath }

// ValidateConfig 当面付必须配置应用私钥才能预下单。
func (AlipayF2F) ValidateConfig(cfg map[string]string) error {
	if strings.TrimSpace(cfg["private_key"]) == "" {
		return fmt.Errorf("支付宝当面付必须填写应用私钥")
	}
	return nil
}

// alipayTimezone 支付宝 OpenAPI 的 timestamp 官方规范为北京时间（GMT+8），
// 不能用服务器本地时区（UTC 容器部署时会因时间偏差被网关拒绝）。
var alipayTimezone = time.FixedZone("GMT+8", 8*3600)

func alipayTimestamp() string {
	return time.Now().In(alipayTimezone).Format("2006-01-02 15:04:05")
}

// alipayPost 公共请求通道：参数校验 → 公共参数组装 → RSA2 签名 → POST → 读取响应体。
// op 为操作名（如"订单查询"/"创建二维码"），仅用于错误文案；extra 为额外公共参数（可为 nil）。
func alipayPost(ctx context.Context, cfg map[string]string, method string, biz []byte, extra url.Values, op string) ([]byte, error) {
	appID := strings.TrimSpace(cfg["app_id"])
	privateKey := strings.TrimSpace(cfg["private_key"])
	if appID == "" || privateKey == "" {
		return nil, fmt.Errorf("支付宝当面付未配置完整（需要 app_id/private_key）")
	}
	endpoint := strings.TrimRight(cfg["api_url"], "/")
	if endpoint == "" {
		endpoint = "https://openapi.alipay.com/gateway.do"
	}
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("支付宝商户私钥无效: %w", err)
	}
	params := url.Values{
		"app_id": {appID}, "method": {method}, "format": {"json"},
		"charset": {"UTF-8"}, "sign_type": {"RSA2"}, "timestamp": {alipayTimestamp()},
		"version": {"1.0"}, "biz_content": {string(biz)},
	}
	for k, vs := range extra {
		for _, v := range vs {
			params.Add(k, v)
		}
	}
	sign, err := signAlipay(params, key)
	if err != nil {
		return nil, fmt.Errorf("支付宝%s签名失败: %w", op, err)
	}
	params.Set("sign", sign)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求支付宝%s失败: %w", op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("支付宝接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取支付宝响应失败: %w", err)
	}
	return body, nil
}

// QueryOrder 通过 alipay.trade.query 主动查询订单，用于内网等收不到异步通知
// 的场景补单。结果仍由调用方再次校验订单号与金额后才核销。
func (AlipayF2F) QueryOrder(ctx context.Context, req QueryOrderRequest) (QueryOrderResult, error) {
	if req.InvoiceNo == "" {
		return QueryOrderResult{}, fmt.Errorf("支付宝当面付订单查询参数不完整")
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": req.InvoiceNo})
	body, err := alipayPost(ctx, req.Config, "alipay.trade.query", biz, nil, "订单查询")
	if err != nil {
		return QueryOrderResult{}, err
	}
	var envelope struct {
		Response json.RawMessage `json:"alipay_trade_query_response"`
		Sign     string          `json:"sign"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Response) == 0 {
		return QueryOrderResult{}, fmt.Errorf("支付宝订单查询响应格式无效")
	}
	// 配置了支付宝公钥时校验响应签名，防止响应被篡改。
	if publicKey := strings.TrimSpace(req.Config["public_key"]); publicKey != "" {
		pub, perr := parsePublicKey(publicKey)
		if perr != nil {
			return QueryOrderResult{}, fmt.Errorf("支付宝公钥无效: %w", perr)
		}
		if envelope.Sign == "" {
			return QueryOrderResult{}, fmt.Errorf("支付宝订单查询响应缺少签名")
		}
		signature, derr := base64.StdEncoding.DecodeString(envelope.Sign)
		digest := sha256.Sum256(envelope.Response)
		if derr != nil || rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) != nil {
			return QueryOrderResult{}, fmt.Errorf("支付宝订单查询响应验签失败")
		}
	}
	var result struct {
		Code        string `json:"code"`
		Msg         string `json:"msg"`
		SubMsg      string `json:"sub_msg"`
		TradeNo     string `json:"trade_no"`
		OutTradeNo  string `json:"out_trade_no"`
		TradeStatus string `json:"trade_status"`
		TotalAmount string `json:"total_amount"`
	}
	if err := json.Unmarshal(envelope.Response, &result); err != nil {
		return QueryOrderResult{}, fmt.Errorf("支付宝订单查询响应解析失败: %w", err)
	}
	if result.Code != "10000" {
		return QueryOrderResult{}, fmt.Errorf("支付宝订单查询失败: %s %s", result.Msg, result.SubMsg)
	}
	paid := result.TradeStatus == "TRADE_SUCCESS" || result.TradeStatus == "TRADE_FINISHED"
	return QueryOrderResult{TradeNo: result.TradeNo, OutTradeNo: result.OutTradeNo, Amount: result.TotalAmount, Paid: paid}, nil
}

func (AlipayF2F) PayURL(ctx context.Context, req PayRequest) (string, error) {
	biz, _ := json.Marshal(map[string]string{"out_trade_no": req.InvoiceNo, "total_amount": req.Amount, "subject": req.Title})
	body, err := alipayPost(ctx, req.Config, "alipay.trade.precreate", biz, url.Values{"notify_url": {req.NotifyURL}}, "创建二维码")
	if err != nil {
		return "", err
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
	return LocalCheckoutPath + "?invoice=" + url.QueryEscape(req.InvoiceNo) + "&data=" + url.QueryEscape(result.Response.QRCode), nil
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
	// 与支付宝 rsaCheckV1 一致：验签内容剔除 sign 与 sign_type
	values.Del("sign")
	values.Del("sign_type")
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
