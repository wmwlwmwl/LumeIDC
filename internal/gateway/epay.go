package gateway

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 易支付 payment_mode 配置值
const (
	epayModeRedirect = "redirect" // 默认：submit.php 跳转托管页
	epayModeQRCode   = "qrcode"   // mapi.php API 模式，返回二维码渲染到本地结算页
)

// epayHTTPClient 包级单例，复用连接池。外层 QueryOrder 已有 context.WithTimeout(15s)，
// 这里 20s 作为兜底上限（ctx 先到期就先取消）。
var epayHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	},
}

// Epay 实现易支付（彩虹易支付）标准提交协议：md5 签名、GET 跳转。
type Epay struct{}

type epayOrder struct {
	Code       int    `json:"code"`
	Msg        string `json:"msg"`
	TradeNo    string `json:"trade_no"`
	OutTradeNo string `json:"out_trade_no"`
	Money      string `json:"money"`
	Status     int    `json:"status"`
}

func (Epay) Driver() string { return "epay" }
func (Epay) Name() string   { return "易支付" }

// CheckoutPath 声明易支付可使用本地二维码结算页。
// 实际是否走本地结算页由 PayURL 内部按 payment_mode 决定，
// 实现此接口仅让 /pay/qr 的安全校验放行对应账单。
func (Epay) CheckoutPath() string { return LocalCheckoutPath }

// ValidateConfig 易支付启用前必须配置完整的商户凭据，避免空密钥导致验签失效；
// 支付渠道同为下单必填项（submit.php 的 type），缺失时能启用但用户点支付必失败。
func (Epay) ValidateConfig(cfg map[string]string) error {
	if strings.TrimSpace(cfg["api_url"]) == "" || strings.TrimSpace(cfg["pid"]) == "" || strings.TrimSpace(cfg["channel"]) == "" || strings.TrimSpace(cfg["key"]) == "" {
		return fmt.Errorf("易支付必须填写 API 地址、商户 PID、支付渠道和商户密钥")
	}
	return nil
}

// sanitizeURLError 去掉传输错误里 URL 的查询串。
//
// 易支付查单按上游协议把商户密钥放在查询串（无法改），而 *url.Error 会打印完整 URL，
// 直接 %w 上抛会把密钥写进服务端日志与管理员告警邮件。保留 scheme://host/path 便于排查，
// 只丢弃查询串。
func sanitizeURLError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	if u, perr := url.Parse(uerr.URL); perr == nil && u.RawQuery != "" {
		u.RawQuery = ""
		uerr.URL = u.String()
	}
	return uerr
}

// QueryOrder queries an order without changing local state. The caller must
// still validate the returned order number and amount before marking it paid.
func (Epay) QueryOrder(ctx context.Context, req QueryOrderRequest) (QueryOrderResult, error) {
	apiURL := req.Config["api_url"]
	pid := req.Config["pid"]
	key := req.Config["key"]
	outTradeNo := req.InvoiceNo
	if apiURL == "" || pid == "" || key == "" || outTradeNo == "" {
		return QueryOrderResult{}, fmt.Errorf("易支付订单查询参数不完整")
	}
	values := url.Values{"act": {"order"}, "pid": {pid}, "key": {key}, "out_trade_no": {outTradeNo}}
	endpoint := strings.TrimRight(apiURL, "/") + "/api.php?" + values.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return QueryOrderResult{}, err
	}
	resp, err := epayHTTPClient.Do(httpReq)
	if err != nil {
		return QueryOrderResult{}, fmt.Errorf("请求易支付订单查询失败: %w", sanitizeURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return QueryOrderResult{}, fmt.Errorf("读取易支付订单查询失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return QueryOrderResult{}, fmt.Errorf("易支付订单查询 HTTP %d", resp.StatusCode)
	}
	var order epayOrder
	if err := json.Unmarshal(body, &order); err != nil {
		return QueryOrderResult{}, fmt.Errorf("易支付订单查询响应无效: %w", err)
	}
	if order.Code != 1 {
		return QueryOrderResult{}, fmt.Errorf("易支付订单查询失败: %s", order.Msg)
	}
	return QueryOrderResult{TradeNo: order.TradeNo, OutTradeNo: order.OutTradeNo, Amount: order.Money, Paid: order.Status == 1}, nil
}

func (Epay) PayURL(ctx context.Context, req PayRequest) (PayResult, error) {
	api := req.Config["api_url"]
	pid := req.Config["pid"]
	key := req.Config["key"]
	channel := req.Config["channel"]
	if api == "" || pid == "" || key == "" || channel == "" {
		return PayResult{}, fmt.Errorf("易支付未配置完整（需要 api_url/pid/key/channel）")
	}
	mode := strings.TrimSpace(req.Config["payment_mode"])
	if mode == epayModeQRCode {
		return epayQRCodePayURL(ctx, api, pid, key, channel, req)
	}
	// 默认 redirect：submit.php 跳转托管页
	params := map[string]string{
		"pid":          pid,
		"type":         channel,
		"out_trade_no": req.InvoiceNo,
		"notify_url":   req.NotifyURL,
		"return_url":   req.ReturnURL,
		"name":         req.Title,
		"money":        req.Amount,
	}
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("sign", md5Sign(q, key))
	q.Set("sign_type", "MD5")
	api = strings.TrimRight(api, "/")
	return PayResult{URL: api + "/submit.php?" + q.Encode()}, nil
}

// epayQRCodePayURL 调用 mapi.php API 获取二维码内容，返回本地结算页路径。
// 对齐 sub2api 的 EasyPay.createAPIPayment：解析 qrcode 用于渲染，
// 解析 money 用于风控浮动后的实际支付金额（部分易支付分支会调整）。
func epayQRCodePayURL(ctx context.Context, api, pid, key, channel string, req PayRequest) (PayResult, error) {
	params := map[string]string{
		"pid":          pid,
		"type":         channel,
		"out_trade_no": req.InvoiceNo,
		"notify_url":   req.NotifyURL,
		"return_url":   req.ReturnURL,
		"name":         req.Title,
		"money":        req.Amount,
	}
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("sign", md5Sign(q, key))
	q.Set("sign_type", "MD5")
	api = strings.TrimRight(api, "/")
	endpoint := api + "/mapi.php"

	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return PayResult{}, fmt.Errorf("易支付 mapi 请求构造失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := epayHTTPClient.Do(httpReq)
	if err != nil {
		return PayResult{}, fmt.Errorf("请求易支付 mapi 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return PayResult{}, fmt.Errorf("读取易支付 mapi 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PayResult{}, fmt.Errorf("易支付 mapi 返回 HTTP %d", resp.StatusCode)
	}
	var mapiResp struct {
		Code   int    `json:"code"`
		Msg    string `json:"msg"`
		QRCode string `json:"qrcode"`
		Money  string `json:"money"`
	}
	if err := json.Unmarshal(body, &mapiResp); err != nil {
		return PayResult{}, fmt.Errorf("易支付 mapi 响应解析失败: %w", err)
	}
	if mapiResp.Code != 1 {
		msg := strings.TrimSpace(mapiResp.Msg)
		if msg == "" {
			msg = "未知错误"
		}
		return PayResult{}, fmt.Errorf("易支付 mapi 下单失败: %s", msg)
	}
	qrText := strings.TrimSpace(mapiResp.QRCode)
	if qrText == "" {
		return PayResult{}, fmt.Errorf("易支付 mapi 未返回二维码内容")
	}
	qrText = resolveEpayRelativeRef(api, qrText)
	result := PayResult{URL: LocalCheckoutPath + "?invoice=" + url.QueryEscape(req.InvoiceNo) + "&data=" + url.QueryEscape(qrText)}
	// 部分易支付分支为风控会对金额做微小浮动（如 10.00 → 10.01），
	// 这里把实际金额带回去让调用方更新 payment_attempts.amount，
	// 否则回调时 equalAmount 对不上会导致核销失败。
	if money := strings.TrimSpace(mapiResp.Money); money != "" {
		result.Amount = money
	}
	return result, nil
}

// resolveEpayRelativeRef 将 mapi.php 返回的以 "/" 开头的相对路径解析为绝对 URL。
// 部分易支付分支返回相对路径（如 "/api/pay/toapp/xxx"），直接渲染会变成无效二维码。
// 以 scheme 开头的（https://、weixin://、alipays://）已经可用，原样返回。
func resolveEpayRelativeRef(apiBase, ref string) string {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(trimmed, "/") {
		return ref
	}
	base, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ref
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "" {
		return ref
	}
	return base.ResolveReference(parsed).String()
}

func (e Epay) VerifyNotify(req NotifyRequest, cfg map[string]string) (NotifyResult, error) {
	params := req.Params
	key := cfg["key"]
	if strings.TrimSpace(key) == "" {
		// 空密钥下 md5(参数串 + "") 可被任意伪造，必须按失败关闭。
		return NotifyResult{}, fmt.Errorf("易支付未配置商户密钥")
	}
	invoiceNo, tradeNo, ok := verifyEpaySign(params, key)
	if !ok {
		return NotifyResult{}, fmt.Errorf("易支付回调签名校验失败")
	}
	// 验签通过后再确认「该通知属于本商户」：同一密钥被多站点共用时，仅验签
	// 无法区分通知归属。双方都带 pid 且不一致才拒绝——部分易支付分支的通知
	// 不携带 pid，不能因此误杀，此时仍由密钥隔离兜底。
	// ponytail: 上游不回传 pid 时该校验自动跳过，升级路径是要求上游回传 pid 后改为强制比对。
	if pid := cfg["pid"]; strings.TrimSpace(pid) != "" && params["pid"] != "" && params["pid"] != pid {
		return NotifyResult{}, fmt.Errorf("易支付回调商户号不匹配")
	}
	if params["trade_status"] != "TRADE_SUCCESS" && params["trade_status"] != "TRADE_FINISHED" {
		return NotifyResult{InvoiceNo: invoiceNo, TradeNo: tradeNo}, nil
	}
	if tradeNo == "" || params["money"] == "" {
		return NotifyResult{}, fmt.Errorf("易支付回调缺少流水号或金额")
	}
	return NotifyResult{InvoiceNo: invoiceNo, TradeNo: tradeNo, Amount: params["money"], Successful: true}, nil
}

// verifyEpaySign 校验易支付异步通知签名。params 为回调全部 GET 参数。
// 返回商户订单号(out_trade_no)与平台流水号(trade_no)。
// 不同易支付分支对“空值是否参与签名”实现不一，这里同时尝试两种规则，任一匹配即通过。
// （与方法 Epay.VerifyNotify 同名易混，故包级函数带 verify 前缀区分。）
func verifyEpaySign(params map[string]string, key string) (invoiceNo, tradeNo string, ok bool) {
	sign := params["sign"]
	if sign == "" || params["out_trade_no"] == "" {
		return "", "", false
	}
	q := url.Values{}
	for k, v := range params {
		switch k {
		case "sign", "sign_type", "":
			continue
		}
		q.Set(k, v)
	}
	if !strings.EqualFold(md5Sign(q, key), sign) && !strings.EqualFold(md5SignAll(q, key), sign) {
		return "", "", false
	}
	return params["out_trade_no"], params["trade_no"], true
}

// md5Sign: 按 ASCII 键名排序拼接 a=b&c=d...，再直接拼接商户密钥取 md5 小写，跳过空值。
func md5Sign(q url.Values, key string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		if k == "sign" || k == "sign_type" || q.Get(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	return md5SignKeys(q, keys, key)
}

// md5SignAll: 与 md5Sign 相同，但空值也参与签名（兼容部分易支付分支）。
func md5SignAll(q url.Values, key string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	return md5SignKeys(q, keys, key)
}

func md5SignKeys(q url.Values, keys []string, key string) string {
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + q.Get(k)
	}
	raw := strings.Join(parts, "&") + key
	sum := md5.Sum([]byte(raw))
	return fmt.Sprintf("%x", sum)
}
