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

// alipayHTTPClient 包级单例，复用连接池。
var alipayHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	},
}

// Alipay 实现支付宝支付（RSA2）：默认走当面付预下单，二维码渲染到本站本地
// 结算页；配置 payment_mode=redirect 时改走电脑/手机网站支付，跳转支付宝收银台。
type Alipay struct{}

// 支付宝 payment_mode 配置值
const (
	// alipayModeRedirect 跳转支付宝收银台：电脑端 page.pay、手机端 wap.pay。
	// 空值或其它值为扫码模式：电脑端用当面付二维码（本站结算页），
	// 手机端默认改跳 wap.pay，可用 mobile_qrcode=1 强制手机端也用二维码。
	alipayModeRedirect = "redirect"
)

// 支付宝产品码（product_code）
const (
	alipayProductPrecreate = "FACE_TO_FACE_PAYMENT"   // 当面付
	alipayProductPagePay   = "FAST_INSTANT_TRADE_PAY" // 电脑网站支付
	alipayProductWapPay    = "QUICK_WAP_WAY"          // 手机网站支付
)

func (Alipay) Driver() string { return "alipay" }
func (Alipay) Name() string   { return "支付宝" }

// CheckoutPath 声明支付宝可使用本站的本地二维码结算页（当面付模式）。
// 跳转模式（payment_mode=redirect）不经过该页，直接跳支付宝收银台。
func (Alipay) CheckoutPath() string { return LocalCheckoutPath }

// ValidateConfig 支付宝必须先配置应用私钥才能下单（当面付预下单与网站支付签名都需要）。
func (Alipay) ValidateConfig(cfg map[string]string) error {
	if strings.TrimSpace(cfg["private_key"]) == "" {
		return fmt.Errorf("支付宝必须填写应用私钥")
	}
	return nil
}

// alipayTimezone 支付宝 OpenAPI 的 timestamp 官方规范为北京时间（GMT+8），
// 不能用服务器本地时区（UTC 容器部署时会因时间偏差被网关拒绝）。
var alipayTimezone = time.FixedZone("GMT+8", 8*3600)

func alipayTimestamp() string {
	return time.Now().In(alipayTimezone).Format("2006-01-02 15:04:05")
}

// alipaySignedParams 组装并 RSA2 签名支付宝请求参数（不发起请求）。
// op 为操作名（如"订单查询"/"创建二维码"），仅用于错误文案；extra 为额外公共参数（可为 nil）。
// page.pay/wap.pay 等页面跳转接口只需本地拼装该签名参数、无需服务端调用，故一并抽出复用。
func alipaySignedParams(cfg map[string]string, method string, biz []byte, extra url.Values, op string) (url.Values, string, error) {
	appID := strings.TrimSpace(cfg["app_id"])
	privateKey := strings.TrimSpace(cfg["private_key"])
	if appID == "" || privateKey == "" {
		return nil, "", fmt.Errorf("支付宝未配置完整（需要 app_id/private_key）")
	}
	endpoint := strings.TrimRight(cfg["api_url"], "/")
	if endpoint == "" {
		endpoint = "https://openapi.alipay.com/gateway.do"
	}
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("支付宝商户私钥无效: %w", err)
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
		return nil, "", fmt.Errorf("支付宝%s签名失败: %w", op, err)
	}
	params.Set("sign", sign)
	return params, endpoint, nil
}

// alipayPost 公共请求通道：组装签名参数 → POST → 读取响应体。
func alipayPost(ctx context.Context, cfg map[string]string, method string, biz []byte, extra url.Values, op string) ([]byte, error) {
	params, endpoint, err := alipaySignedParams(cfg, method, biz, extra, op)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	resp, err := alipayHTTPClient.Do(request)
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
func (Alipay) QueryOrder(ctx context.Context, req QueryOrderRequest) (QueryOrderResult, error) {
	if req.InvoiceNo == "" {
		return QueryOrderResult{}, fmt.Errorf("支付宝订单查询参数不完整")
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

func (Alipay) PayURL(ctx context.Context, req PayRequest) (PayResult, error) {
	// 跳转模式：电脑/手机都直接打开支付宝收银台。
	if strings.TrimSpace(req.Config["payment_mode"]) == alipayModeRedirect {
		return alipayCheckoutURL(req)
	}
	// 扫码模式：电脑走当面付二维码渲染到本站结算页；手机端默认改走手机网站支付，
	// 避免用户在手机上看到一个自己扫不了的二维码。商户未签约「手机网站支付」时
	// 可置 mobile_qrcode=1 强制手机端也用二维码。
	if req.IsMobile && !alipayMobileForcesQRCode(req.Config) {
		return alipayCheckoutURL(req)
	}
	return alipayPrecreatePayURL(ctx, req)
}

// alipayMobileForcesQRCode 手机端是否强制使用二维码（配置 mobile_qrcode=1）。
func alipayMobileForcesQRCode(cfg map[string]string) bool {
	return strings.TrimSpace(cfg["mobile_qrcode"]) == "1"
}

// alipayPrecreatePayURL 当面付预下单，返回本站二维码结算页地址。
func alipayPrecreatePayURL(ctx context.Context, req PayRequest) (PayResult, error) {
	biz, _ := json.Marshal(map[string]string{
		"out_trade_no": req.InvoiceNo, "total_amount": req.Amount,
		"subject": req.Title, "product_code": alipayProductPrecreate,
	})
	body, err := alipayPost(ctx, req.Config, "alipay.trade.precreate", biz, url.Values{"notify_url": {req.NotifyURL}}, "创建二维码")
	if err != nil {
		return PayResult{}, err
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
		return PayResult{}, fmt.Errorf("支付宝响应格式无效")
	}
	if result.Response.Code != "10000" || result.Response.QRCode == "" {
		return PayResult{}, fmt.Errorf("支付宝创建二维码失败: %s %s", result.Response.Msg, result.Response.SubMsg)
	}
	return PayResult{URL: LocalCheckoutPath + "?invoice=" + url.QueryEscape(req.InvoiceNo) + "&data=" + url.QueryEscape(result.Response.QRCode)}, nil
}

// alipayCheckoutURL 拼装支付宝收银台跳转地址：手机走 wap.pay，电脑走 page.pay。
// 两者都是页面跳转接口——本地签名拼装 URL 让浏览器跳转即可，无需服务端调用。
// 异步通知与验签规则同当面付，共用 VerifyNotify。
//
// 前置条件：商户需在支付宝签约对应产品（电脑网站支付 / 手机网站支付），
// 否则支付宝收银台会提示 ISV 权限不足。
func alipayCheckoutURL(req PayRequest) (PayResult, error) {
	method, productCode := "alipay.trade.page.pay", alipayProductPagePay
	if req.IsMobile {
		method, productCode = "alipay.trade.wap.pay", alipayProductWapPay
	}
	biz, _ := json.Marshal(map[string]string{
		"out_trade_no": req.InvoiceNo, "total_amount": req.Amount,
		"subject": req.Title, "product_code": productCode,
	})
	// return_url / notify_url 属公共参数（非 biz_content），与支付宝文档一致。
	params, endpoint, err := alipaySignedParams(req.Config, method, biz,
		url.Values{"notify_url": {req.NotifyURL}, "return_url": {req.ReturnURL}}, "生成支付链接")
	if err != nil {
		return PayResult{}, err
	}
	return PayResult{URL: endpoint + "?" + params.Encode()}, nil
}

func (Alipay) VerifyNotify(req NotifyRequest, cfg map[string]string) (NotifyResult, error) {
	params := req.Params
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
	// 验签通过后再确认「该通知属于本应用」：同一账号下的多个应用可共用支付宝公钥，
	// 仅靠验签无法区分通知归属（同账号多应用场景下可被互相顶单）。
	// 双方都带 app_id 且不一致才拒绝——老版本通知可能不带，不能因此误杀。
	// ponytail: 通知不带 app_id 时该校验自动跳过，仍由公钥隔离兜底。
	if appID := cfg["app_id"]; strings.TrimSpace(appID) != "" && params["app_id"] != "" && params["app_id"] != appID {
		return NotifyResult{}, fmt.Errorf("支付宝回调应用 ID 不匹配")
	}
	// 收款账号（seller_id / PID）同样属本商户：一个支付宝账号可开多个应用，
	// 只比 app_id 挡不住「同一账号下另一应用」的通知；两处都配齐后互为交叉校验。
	// 未配置 seller_id 时跳过，不引入强制填写要求。
	if sellerID := cfg["seller_id"]; strings.TrimSpace(sellerID) != "" && params["seller_id"] != "" && params["seller_id"] != sellerID {
		return NotifyResult{}, fmt.Errorf("支付宝回调收款账号不匹配")
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
