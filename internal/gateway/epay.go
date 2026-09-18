package gateway

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
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

// ValidateConfig 易支付启用前必须配置完整的商户凭据，避免空密钥导致验签失效；
// 支付渠道同为下单必填项（submit.php 的 type），缺失时能启用但用户点支付必失败。
func (Epay) ValidateConfig(cfg map[string]string) error {
	if strings.TrimSpace(cfg["api_url"]) == "" || strings.TrimSpace(cfg["pid"]) == "" || strings.TrimSpace(cfg["channel"]) == "" || strings.TrimSpace(cfg["key"]) == "" {
		return fmt.Errorf("易支付必须填写 API 地址、商户 PID、支付渠道和商户密钥")
	}
	return nil
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
		return QueryOrderResult{}, fmt.Errorf("请求易支付订单查询失败: %w", err)
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

func (Epay) PayURL(ctx context.Context, req PayRequest) (string, error) {
	api := req.Config["api_url"]
	pid := req.Config["pid"]
	key := req.Config["key"]
	channel := req.Config["channel"]
	if api == "" || pid == "" || key == "" || channel == "" {
		return "", fmt.Errorf("易支付未配置完整（需要 api_url/pid/key/channel）")
	}
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
	return api + "/submit.php?" + q.Encode(), nil
}

func (e Epay) VerifyNotify(params map[string]string, cfg map[string]string) (NotifyResult, error) {
	invoiceNo, tradeNo, ok := verifyEpaySign(params, cfg["key"])
	if !ok {
		return NotifyResult{}, fmt.Errorf("易支付回调签名校验失败")
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
