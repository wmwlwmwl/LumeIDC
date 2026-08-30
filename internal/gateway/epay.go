package gateway

import (
	"context"
	"crypto/md5"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Epay 实现易支付（彩虹易支付）标准提交协议：md5 签名、GET 跳转。
type Epay struct{}

func (Epay) Driver() string { return "epay" }
func (Epay) Name() string   { return "易支付" }

func (Epay) PayURL(ctx context.Context, req PayRequest) (string, error) {
	api := req.Config["api_url"]
	pid := req.Config["pid"]
	key := req.Config["key"]
	if api == "" || pid == "" || key == "" {
		return "", fmt.Errorf("易支付未配置完整（需要 api_url/pid/key）")
	}
	params := map[string]string{
		"pid":          pid,
		"type":         req.Config["channel"],
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
	invoiceNo, tradeNo, ok := VerifyNotify(params, cfg["key"])
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

// VerifyNotify 校验易支付异步通知签名。params 为回调全部 GET 参数。
// 返回商户订单号(out_trade_no)与平台流水号(trade_no)。
// 不同易支付分支对“空值是否参与签名”实现不一，这里同时尝试两种规则，任一匹配即通过。
func VerifyNotify(params map[string]string, key string) (invoiceNo, tradeNo string, ok bool) {
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

// md5Sign: 按 ASCII 键名排序拼接 a=b&c=d... 再拼 &KEY=key 取 md5 小写，跳过空值。
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
	raw := strings.Join(parts, "&") + "&key=" + key
	sum := md5.Sum([]byte(raw))
	return fmt.Sprintf("%x", sum)
}
