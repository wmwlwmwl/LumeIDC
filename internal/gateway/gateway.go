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
	// VerifyNotify 校验并解析一笔异步回调。表单/查询类网关（易支付、支付宝）
	// 读 NotifyRequest.Params；JSON + 请求头验签类网关（微信 APIv3）读
	// RawBody 与 Headers。
	VerifyNotify(req NotifyRequest, cfg map[string]string) (NotifyResult, error)
}

// NotifyRequest 是网关回调的原始输入。三种载荷形态不同，故分别携带，
// 由各驱动按自身协议取用。
type NotifyRequest struct {
	// Params 表单或查询参数（已剔除本站自身的路由参数，如区分实例的 code）。
	Params map[string]string
	// RawBody 原始请求体。JSON 类回调（微信 APIv3）的验签与解密都必须基于原始字节。
	RawBody string
	// Headers 请求头，键名为小写。微信 APIv3 的签名放在请求头而非载荷里。
	Headers map[string]string
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

// NotifyAcker is an optional gateway capability that customises the body
// returned to the gateway after a notification was accepted. The default is
// "success" (易支付/支付宝); 微信 APIv3 要求应答 JSON，否则会持续重试。
type NotifyAcker interface {
	// NotifyAck returns the response body acking an accepted notification.
	NotifyAck() string
}

// NotifyURLBuilder is an optional gateway capability that customises the async
// callback URL. The default is {base}/pay/notify?code=<编码>；微信 APIv3 明确
// 禁止回调地址携带查询参数，须改用路径段承载网关编码。
type NotifyURLBuilder interface {
	NotifyURL(base, code string) string
}

// UserFacingError 表示驱动给出的、可以直接展示给用户的提示（而非内部故障）。
// 调用方遇到它时应原样透传消息，而不是替换成笼统的"生成支付链接失败"。
type UserFacingError struct {
	Msg string
}

func (e UserFacingError) Error() string { return e.Msg }

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
	ClientIP  string // 用户 IP，微信 H5 支付为必填
	// IsMobile 请求来自移动端浏览器。用于选择 H5 支付通道
	// （如支付宝手机网站支付 wap.pay、微信 H5 支付）。
	IsMobile bool
	// IsWeChatBrowser 请求来自微信内置浏览器。微信内无法使用 H5 支付，
	// 而 JSAPI 需要公众号授权（尚未实现），故仅用于给出明确提示。
	IsWeChatBrowser bool
	Config          map[string]string
}
