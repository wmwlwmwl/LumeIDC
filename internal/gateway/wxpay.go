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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	moneyutil "lumeidc/internal/money"
)

// 微信支付 APIv3 端点与常量。
const (
	wxpayAPIBase        = "https://api.mch.weixin.qq.com"
	wxpayNativePath     = "/v3/pay/transactions/native"
	wxpayH5Path         = "/v3/pay/transactions/h5"
	wxpayQueryPath      = "/v3/pay/transactions/out-trade-no/"
	wxpayCurrency       = "CNY"
	wxpayH5TradeType    = "Wap"
	wxpayTradeStatePaid = "SUCCESS"
	wxpayEventPaid      = "TRANSACTION.SUCCESS"
	wxpayGCMAlgorithm   = "AEAD_AES_256_GCM"
	wxpayAPIV3KeyLen    = 32
	wxpayHTTPTimeout    = 15 * time.Second
	wxpayMaxRespSize    = 1 << 20
	wxpayUserAgent      = "lumeidc"
	wxpayAuthScheme     = "WECHATPAY2-SHA256-RSA2048"
)

// 微信支付配置键（与后台表单字段名一致）。
const (
	wxpayCfgAPIURL      = "api_url"
	wxpayCfgAppID       = "app_id"
	wxpayCfgMchID       = "mch_id"
	wxpayCfgPrivateKey  = "private_key"
	wxpayCfgAPIV3Key    = "api_v3_key"
	wxpayCfgCertSerial  = "cert_serial"
	wxpayCfgPublicKey   = "public_key"
	wxpayCfgPublicKeyID = "public_key_id"
	wxpayCfgH5AppName   = "h5_app_name"
	wxpayCfgH5AppURL    = "h5_app_url"
)

// Wxpay 实现微信支付 APIv3：电脑端 Native 扫码（复用本站二维码结算页），
// 手机端 H5 支付跳转。验签采用「微信支付公钥」模式——微信自 2024-10 起不再
// 为新商户提供平台证书下载，公钥模式是唯一的通用选择。
//
// 未实现的能力：退款、关单、JSAPI（微信内支付需公众号网页授权）。
type Wxpay struct{}

func (Wxpay) Driver() string { return "wxpay" }

// init 自注册到网关注册表（新增网关照此一行接入，组合根无需改动）。
func init() { Register(Wxpay{}) }
func (Wxpay) Name() string   { return "微信支付" }

// CheckoutPath 声明微信 Native 支付复用本站的本地二维码结算页。
func (Wxpay) CheckoutPath() string { return LocalCheckoutPath }

// NotifyAck 微信要求应答 JSON，返回纯文本会被判为失败并持续重试。
func (Wxpay) NotifyAck() string { return `{"code":"SUCCESS","message":"成功"}` }

// NotifyURL 微信明确禁止回调地址携带查询参数（"notify_url不能携带参数"），
// 故改用路径段承载网关编码：{base}/pay/{code}/notify。
func (Wxpay) NotifyURL(base, code string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/pay/" + url.PathEscape(code) + "/notify"
}

// ValidateConfig 微信支付凭据缺一不可，且密钥格式与 APIv3 密钥长度需在保存时即可发现。
func (Wxpay) ValidateConfig(cfg map[string]string) error {
	for _, f := range []struct{ key, label string }{
		{wxpayCfgAppID, "微信 AppID"},
		{wxpayCfgMchID, "商户号"},
		{wxpayCfgPrivateKey, "商户 API 私钥"},
		{wxpayCfgAPIV3Key, "APIv3 密钥"},
		{wxpayCfgCertSerial, "商户证书序列号"},
		{wxpayCfgPublicKey, "微信支付公钥"},
		{wxpayCfgPublicKeyID, "微信支付公钥 ID"},
	} {
		if strings.TrimSpace(cfg[f.key]) == "" {
			return fmt.Errorf("微信支付必须填写%s", f.label)
		}
	}
	if n := len(strings.TrimSpace(cfg[wxpayCfgAPIV3Key])); n != wxpayAPIV3KeyLen {
		return fmt.Errorf("微信支付 APIv3 密钥必须为 %d 位（当前 %d 位）", wxpayAPIV3KeyLen, n)
	}
	if _, err := parsePrivateKey(cfg[wxpayCfgPrivateKey]); err != nil {
		return fmt.Errorf("微信支付商户 API 私钥无效: %w", err)
	}
	if _, err := parsePublicKey(cfg[wxpayCfgPublicKey]); err != nil {
		return fmt.Errorf("微信支付公钥无效: %w", err)
	}
	return nil
}

// PayURL 按设备分流：手机走 H5 支付，电脑走 Native 扫码。
// 微信支付没有"电脑端网页跳转收银台"这类形态，故不提供支付模式开关。
func (Wxpay) PayURL(ctx context.Context, req PayRequest) (PayResult, error) {
	// 微信内置浏览器既不支持 H5 支付（官方限制），也没有可用的 JSAPI 通道，
	// 直接给出可执行的引导而不是让用户在收银台页面上碰壁。
	if req.IsWeChatBrowser {
		return PayResult{}, UserFacingError{Msg: "微信内暂不支持发起支付，请点击右上角改用手机浏览器或电脑打开本页"}
	}
	c, err := newWxpayClient(req.Config)
	if err != nil {
		return PayResult{}, err
	}
	if req.IsMobile {
		return wxpayH5Pay(ctx, c, req)
	}
	return wxpayNativePay(ctx, c, req)
}

// QueryOrder 通过 APIv3 查单，用于异步通知丢失时的补单。
func (Wxpay) QueryOrder(ctx context.Context, req QueryOrderRequest) (QueryOrderResult, error) {
	if strings.TrimSpace(req.InvoiceNo) == "" {
		return QueryOrderResult{}, fmt.Errorf("微信支付订单查询参数不完整")
	}
	c, err := newWxpayClient(req.Config)
	if err != nil {
		return QueryOrderResult{}, err
	}
	reqPath := wxpayQueryPath + url.PathEscape(req.InvoiceNo) +
		"?mchid=" + url.QueryEscape(c.cfg[wxpayCfgMchID])
	raw, err := c.call(ctx, http.MethodGet, reqPath, "")
	if err != nil {
		return QueryOrderResult{}, err
	}
	var resp struct {
		TransactionID string `json:"transaction_id"`
		OutTradeNo    string `json:"out_trade_no"`
		TradeState    string `json:"trade_state"`
		Amount        struct {
			Total int64 `json:"total"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return QueryOrderResult{}, fmt.Errorf("微信支付查单应答解析失败: %w", err)
	}
	tradeNo := strings.TrimSpace(resp.TransactionID)
	if tradeNo == "" {
		tradeNo = req.InvoiceNo
	}
	return QueryOrderResult{
		TradeNo:    tradeNo,
		OutTradeNo: resp.OutTradeNo,
		Amount:     moneyutil.FormatCents(resp.Amount.Total),
		Paid:       resp.TradeState == wxpayTradeStatePaid,
	}, nil
}

// VerifyNotify 校验微信回调：先按请求头验签，再用 APIv3 密钥解密 resource。
// 验签只依赖微信支付公钥，因此不要求商户私钥可解析。
func (Wxpay) VerifyNotify(req NotifyRequest, cfg map[string]string) (NotifyResult, error) {
	if strings.TrimSpace(req.RawBody) == "" {
		return NotifyResult{}, fmt.Errorf("微信支付回调缺少报文")
	}
	pub, err := parsePublicKey(cfg[wxpayCfgPublicKey])
	if err != nil {
		return NotifyResult{}, fmt.Errorf("微信支付公钥无效: %w", err)
	}
	if err := wxpayVerify(pub,
		req.Headers["wechatpay-timestamp"], req.Headers["wechatpay-nonce"],
		req.Headers["wechatpay-signature"], req.RawBody); err != nil {
		return NotifyResult{}, fmt.Errorf("微信支付回调验签失败: %w", err)
	}
	var notify struct {
		EventType string `json:"event_type"`
		Resource  struct {
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal([]byte(req.RawBody), &notify); err != nil {
		return NotifyResult{}, fmt.Errorf("微信支付回调报文解析失败: %w", err)
	}
	if notify.EventType != wxpayEventPaid {
		// 非支付成功事件（如退款通知）：已验签即受理，但不核销账单。
		return NotifyResult{}, nil
	}
	if notify.Resource.Algorithm != wxpayGCMAlgorithm {
		return NotifyResult{}, fmt.Errorf("微信支付回调加密算法不支持: %s", notify.Resource.Algorithm)
	}
	plain, err := wxpayDecryptResource(cfg[wxpayCfgAPIV3Key],
		notify.Resource.Nonce, notify.Resource.AssociatedData, notify.Resource.Ciphertext)
	if err != nil {
		return NotifyResult{}, err
	}
	var tx struct {
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		TradeState    string `json:"trade_state"`
		MchID         string `json:"mchid"`
		AppID         string `json:"appid"`
		Amount        struct {
			Total int64 `json:"total"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(plain, &tx); err != nil {
		return NotifyResult{}, fmt.Errorf("微信支付回调解密内容解析失败: %w", err)
	}
	// 解密后的报文才带商户身份，需确认「该通知属于本商户」。新版「微信支付公钥」
	// 按商户签发，归属已由验签绑定；旧版平台证书是全商户共用，只能靠这里兜底。
	// 双方都带该字段且不一致才拒绝，缺字段时不误杀。
	if mchID := cfg[wxpayCfgMchID]; strings.TrimSpace(mchID) != "" && tx.MchID != "" && tx.MchID != mchID {
		return NotifyResult{}, fmt.Errorf("微信支付回调商户号不匹配")
	}
	if appID := cfg[wxpayCfgAppID]; strings.TrimSpace(appID) != "" && tx.AppID != "" && tx.AppID != appID {
		return NotifyResult{}, fmt.Errorf("微信支付回调应用 ID 不匹配")
	}
	if strings.TrimSpace(tx.OutTradeNo) == "" {
		return NotifyResult{}, fmt.Errorf("微信支付回调缺少商户订单号")
	}
	if tx.TradeState != wxpayTradeStatePaid {
		return NotifyResult{InvoiceNo: tx.OutTradeNo, TradeNo: tx.TransactionID}, nil
	}
	return NotifyResult{
		InvoiceNo:  tx.OutTradeNo,
		TradeNo:    tx.TransactionID,
		Amount:     moneyutil.FormatCents(tx.Amount.Total),
		Successful: true,
	}, nil
}

// wxpayClient 承载一次调用的凭据与 HTTP 客户端。
type wxpayClient struct {
	cfg  map[string]string
	base string
	key  *rsa.PrivateKey
	pub  *rsa.PublicKey
	http *http.Client
}

func newWxpayClient(cfg map[string]string) (*wxpayClient, error) {
	key, err := parsePrivateKey(cfg[wxpayCfgPrivateKey])
	if err != nil {
		return nil, fmt.Errorf("微信支付商户 API 私钥无效: %w", err)
	}
	pub, err := parsePublicKey(cfg[wxpayCfgPublicKey])
	if err != nil {
		return nil, fmt.Errorf("微信支付公钥无效: %w", err)
	}
	base := strings.TrimRight(strings.TrimSpace(cfg[wxpayCfgAPIURL]), "/")
	if base == "" {
		base = wxpayAPIBase
	}
	return &wxpayClient{
		cfg: cfg, base: base, key: key, pub: pub,
		http: &http.Client{Timeout: wxpayHTTPTimeout},
	}, nil
}

// call 发起带 APIv3 签名的请求，并对成功应答验签。
func (c *wxpayClient) call(ctx context.Context, method, reqPath, body string) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := wxpayNonce()
	signature, err := wxpaySign(c.key, method, reqPath, timestamp, nonce, body)
	if err != nil {
		return nil, fmt.Errorf("微信支付签名失败: %w", err)
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+reqPath, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wxpayUserAgent)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", fmt.Sprintf(
		`%s mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`,
		wxpayAuthScheme, c.cfg[wxpayCfgMchID], nonce, signature, timestamp, c.cfg[wxpayCfgCertSerial]))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求微信支付失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, wxpayMaxRespSize))
	if err != nil {
		return nil, fmt.Errorf("读取微信支付应答失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("微信支付接口返回 HTTP %d: %s", resp.StatusCode, wxpayErrorDetail(raw))
	}
	// 成功应答同样需要验签，防止应答被中间人篡改。
	if err := wxpayVerify(c.pub, resp.Header.Get("Wechatpay-Timestamp"),
		resp.Header.Get("Wechatpay-Nonce"), resp.Header.Get("Wechatpay-Signature"), string(raw)); err != nil {
		return nil, fmt.Errorf("微信支付应答验签失败: %w", err)
	}
	return raw, nil
}

// wxpayNativePay 电脑端 Native 下单，拿到 code_url 后交给本站二维码结算页渲染。
func wxpayNativePay(ctx context.Context, c *wxpayClient, req PayRequest) (PayResult, error) {
	total, err := wxpayTotalFen(req.Amount)
	if err != nil {
		return PayResult{}, err
	}
	body, err := json.Marshal(map[string]any{
		"appid": c.cfg[wxpayCfgAppID], "mchid": c.cfg[wxpayCfgMchID],
		"description": req.Title, "out_trade_no": req.InvoiceNo,
		"notify_url": req.NotifyURL,
		"amount":     map[string]any{"total": total, "currency": wxpayCurrency},
	})
	if err != nil {
		return PayResult{}, err
	}
	raw, err := c.call(ctx, http.MethodPost, wxpayNativePath, string(body))
	if err != nil {
		return PayResult{}, err
	}
	var resp struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return PayResult{}, fmt.Errorf("微信 Native 下单应答解析失败: %w", err)
	}
	codeURL := strings.TrimSpace(resp.CodeURL)
	if codeURL == "" {
		return PayResult{}, fmt.Errorf("微信支付未返回二维码内容")
	}
	return PayResult{URL: LocalCheckoutPath + "?invoice=" + url.QueryEscape(req.InvoiceNo) + "&data=" + url.QueryEscape(codeURL)}, nil
}

// wxpayH5Pay 手机端 H5 下单，返回微信收银台地址。
func wxpayH5Pay(ctx context.Context, c *wxpayClient, req PayRequest) (PayResult, error) {
	clientIP := strings.TrimSpace(req.ClientIP)
	if clientIP == "" {
		return PayResult{}, fmt.Errorf("微信 H5 支付缺少用户 IP")
	}
	total, err := wxpayTotalFen(req.Amount)
	if err != nil {
		return PayResult{}, err
	}
	h5Info := map[string]any{"type": wxpayH5TradeType}
	if v := strings.TrimSpace(c.cfg[wxpayCfgH5AppName]); v != "" {
		h5Info["app_name"] = v
	}
	if v := strings.TrimSpace(c.cfg[wxpayCfgH5AppURL]); v != "" {
		h5Info["app_url"] = v
	}
	body, err := json.Marshal(map[string]any{
		"appid": c.cfg[wxpayCfgAppID], "mchid": c.cfg[wxpayCfgMchID],
		"description": req.Title, "out_trade_no": req.InvoiceNo,
		"notify_url": req.NotifyURL,
		"amount":     map[string]any{"total": total, "currency": wxpayCurrency},
		"scene_info": map[string]any{"payer_client_ip": clientIP, "h5_info": h5Info},
	})
	if err != nil {
		return PayResult{}, err
	}
	raw, err := c.call(ctx, http.MethodPost, wxpayH5Path, string(body))
	if err != nil {
		return PayResult{}, err
	}
	var resp struct {
		H5URL string `json:"h5_url"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return PayResult{}, fmt.Errorf("微信 H5 下单应答解析失败: %w", err)
	}
	h5URL := strings.TrimSpace(resp.H5URL)
	if h5URL == "" {
		return PayResult{}, fmt.Errorf("微信支付未返回 H5 支付链接")
	}
	// 支付完成后跳回账单页；微信要求 redirect_url 必须 URL 编码。
	if returnURL := strings.TrimSpace(req.ReturnURL); returnURL != "" {
		sep := "&"
		if !strings.Contains(h5URL, "?") {
			sep = "?"
		}
		h5URL += sep + "redirect_url=" + url.QueryEscape(returnURL)
	}
	return PayResult{URL: h5URL}, nil
}

// wxpaySign 生成 APIv3 请求签名。
// 待签名串：请求方法\nURL\n时间戳\n随机串\n请求报文主体\n（末尾换行不可省略）。
func wxpaySign(key *rsa.PrivateKey, method, reqPath, timestamp, nonce, body string) (string, error) {
	message := method + "\n" + reqPath + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// wxpayVerify 校验微信支付应答或回调的签名。
// 待验签串：应答时间戳\n应答随机串\n应答报文主体\n（末尾换行不可省略）。
func wxpayVerify(pub *rsa.PublicKey, timestamp, nonce, signature, body string) error {
	if strings.TrimSpace(timestamp) == "" || strings.TrimSpace(nonce) == "" || strings.TrimSpace(signature) == "" {
		return fmt.Errorf("缺少签名请求头（Wechatpay-Timestamp/Nonce/Signature）")
	}
	message := timestamp + "\n" + nonce + "\n" + body + "\n"
	digest := sha256.Sum256([]byte(message))
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("签名不是合法 Base64")
	}
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], raw)
}

// wxpayDecryptResource 解密回调 resource（AEAD_AES_256_GCM）。
// nonce 与 associated_data 由微信在报文中给出，密钥为商户的 APIv3 密钥。
func wxpayDecryptResource(apiV3Key, nonce, associatedData, ciphertext string) ([]byte, error) {
	key := strings.TrimSpace(apiV3Key)
	if len(key) != wxpayAPIV3KeyLen {
		return nil, fmt.Errorf("微信支付 APIv3 密钥必须为 %d 位（当前 %d 位）", wxpayAPIV3KeyLen, len(key))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return nil, fmt.Errorf("微信支付回调密文不是合法 Base64: %w", err)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("微信支付回调解密初始化失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("微信支付回调解密初始化失败: %w", err)
	}
	plain, err := gcm.Open(nil, []byte(nonce), raw, []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("微信支付回调解密失败（请核对 APIv3 密钥）: %w", err)
	}
	return plain, nil
}

// wxpayTotalFen 元转分（微信金额单位为分）。
func wxpayTotalFen(amount string) (int64, error) {
	_, cents, err := moneyutil.ParsePositive(strings.TrimSpace(amount), 999999999999)
	if err != nil {
		return 0, fmt.Errorf("微信支付金额无效: %w", err)
	}
	return cents, nil
}

// wxpayNonce 生成 32 位随机串。
// ponytail: crypto/rand 不可用属环境级故障，退化用时间戳保证仍能签名；
// 届时签名仍合法，仅随机性下降（微信只要求随机串不重复）。
func wxpayNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}

// wxpayErrorDetail 提取微信错误应答中的 code/message，便于定位；无法解析时截断原文。
func wxpayErrorDetail(body []byte) string {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Code != "" {
		return e.Code + ": " + e.Message
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if len([]rune(s)) > 200 {
		return string([]rune(s)[:200]) + "..."
	}
	return s
}
