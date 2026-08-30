package gateway

import "context"

// Gateway is a payment provider. PayURL returns the URL the user should be
// redirected to for payment. Notify handles async callback verification.
type Gateway interface {
	// Driver identifies the plugin implementation, not a configured instance.
	Driver() string
	Name() string
	// PayURL builds a payment redirect for an invoice.
	PayURL(ctx context.Context, req PayRequest) (string, error)
	VerifyNotify(params map[string]string, cfg map[string]string) (NotifyResult, error)
}

type NotifyResult struct {
	InvoiceNo  string
	TradeNo    string
	Amount     string
	Successful bool
}

type PayRequest struct {
	InvoiceNo string // 商户订单号
	Amount    string // 金额字符串，如 "12.50"
	Title     string
	NotifyURL string // 异步回调
	ReturnURL string // 支付完成后跳转
	Config    map[string]string
}
