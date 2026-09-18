package gateway

import "context"

// Gateway is a payment provider. PayURL returns the URL the user should be
// redirected to for payment. Notify handles async callback verification.
type Gateway interface {
	// Driver identifies the plugin implementation, not a configured instance.
	Driver() string
	Name() string
	// PayURL builds a payment redirect for an invoice. Amount field in PayResult
	// is set when the upstream returns an adjusted/final amount (e.g. EasyPay
	// mapi.php risk-control floating); empty means "use the requested amount".
	PayURL(ctx context.Context, req PayRequest) (PayResult, error)
	VerifyNotify(params map[string]string, cfg map[string]string) (NotifyResult, error)
}

// PayResult is what PayURL returns. URL is always set; Amount is optional.
type PayResult struct {
	// URL 跳转/本地结算页地址
	URL string
	// Amount 上游返回的实际金额（如易支付 mapi.php 风控浮动后）；空则用请求金额
	Amount string
}

// LocalCheckoutPath is the in-app route that serves the local checkout page
// (typically a QR code) for gateways implementing LocalCheckout.
const LocalCheckoutPath = "/pay/qr"

// LocalCheckout is an optional gateway capability for gateways that render an
// in-app checkout page instead of redirecting the browser to an external
// payment page. The generic handler serves the page; the gateway owns the QR.
type LocalCheckout interface {
	// CheckoutPath returns the in-app path of the local checkout page.
	CheckoutPath() string
}

// OrderQuerier is an optional gateway capability used to recover payments
// when an asynchronous notification cannot reach the application.
type OrderQuerier interface {
	QueryOrder(ctx context.Context, req QueryOrderRequest) (QueryOrderResult, error)
}

// ConfigValidator is an optional gateway capability that checks provider
// specific configuration before an instance is saved.
type ConfigValidator interface {
	ValidateConfig(cfg map[string]string) error
}

type QueryOrderRequest struct {
	InvoiceNo string
	Config    map[string]string
}

type QueryOrderResult struct {
	TradeNo    string
	OutTradeNo string
	Amount     string
	Paid       bool
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
	// IsMobile 请求来自移动端浏览器。用于选择 H5 支付通道
	// （如支付宝手机网站支付 wap.pay）。
	IsMobile bool
	Config   map[string]string
}
