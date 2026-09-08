package handler

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/gateway"
	"lumeidc/internal/middleware"
	moneyutil "lumeidc/internal/money"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed templates/pay.html
var payFS embed.FS

type Pay struct {
	Orders   *service.Orders
	Payment  *service.Payment
	Products *repo.Products
	Gateways map[string]gateway.Gateway
	GwRepo   *repo.Gateways
	Invoices *repo.Invoices
	Balance  *repo.Balance
	*Deps

	payOnce sync.Once
	payTpl  *template.Template // pay.html 首次渲染后缓存（并发安全）
	payErr  error
}

func (h *Pay) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /order", h.createOrder)
	// 网关编码使用查询参数，避免与 /pay/{invoiceID}/start 的通配符路由冲突。
	mux.HandleFunc("GET /pay/notify", h.notify)
	mux.HandleFunc("POST /pay/notify", h.notify)
	mux.HandleFunc("POST /pay/{invoiceID}/start", h.start)
	mux.HandleFunc("POST /pay/{invoiceID}/balance", h.payByBalance)
	mux.HandleFunc("GET /pay/{invoiceID}", h.payPage)
	mux.HandleFunc("GET /pay/{invoiceID}/status", h.paymentStatus)
	mux.HandleFunc("GET /pay/alipay-f2f", h.alipayF2FPage)
	mux.HandleFunc("GET /mock/pay/{no}", h.mockPayPage)
	mux.HandleFunc("POST /mock/pay/{no}", h.mockConfirm)
}

// alipayF2FPage 在本站生成二维码，避免把支付码交给第三方图片服务。
func (h *Pay) alipayF2FPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	no := strings.TrimSpace(r.URL.Query().Get("invoice"))
	qrText := r.URL.Query().Get("qr")
	if no == "" || qrText == "" {
		http.NotFound(w, r)
		return
	}
	id, status, driver, gatewayCode, baseAmount, qerr := h.Invoices.F2FByNoUser(r.Context(), no, userID)
	if qerr != nil || status != 0 || driver != "alipay_f2f" {
		http.NotFound(w, r)
		return
	}
	attempt, err := h.GwRepo.LatestAttempt(r.Context(), no, gatewayCode, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pngBytes, err := qrcode.Encode(qrText, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, "生成支付二维码失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if _, err := fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>支付宝扫码支付</title><style>body{font-family:system-ui,sans-serif;background:#f6f8fb;color:#182230;text-align:center;padding:40px 16px}.card{max-width:440px;margin:auto;padding:32px 24px;background:#fff;border-radius:18px;box-shadow:0 10px 30px #12263d12}img{width:320px;max-width:100%%;height:auto}.amount{font-size:28px;font-weight:700;margin:12px}</style></head><body><main class="card"><h1>支付宝扫码支付</h1><p>账单号：%s</p><p class="amount">应付金额：￥%s</p><p>账单金额：￥%s</p><img src="%s" alt="支付宝支付二维码"><p id="message" role="status">支付完成后页面会自动检查到账状态</p><p><a href="/pay/%d">返回账单页</a></p></main><script>(function(){var message=document.getElementById('message');function check(){fetch('/pay/%d/status',{credentials:'same-origin'}).then(function(response){if(!response.ok)throw new Error();return response.json()}).then(function(data){if(data.paid){message.textContent='支付成功，正在返回账单页';location.href='/pay/%d';return}setTimeout(check,4000)}).catch(function(){message.textContent='状态检查失败，正在重试';setTimeout(check,5000)})}check()})();</script></body></html>`, template.HTMLEscapeString(no), attempt.Amount, baseAmount, dataURI, id, id, id); err != nil {
		log.Printf("[template] 支付宝二维码页面输出失败: %v", err)
	}
}

func (h *Pay) paymentStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, status, qerr := h.Invoices.StatusByIDUser(r.Context(), id, userID)
	if qerr != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]bool{"paid": status == 1})
}

// createOrder POST product_id & cycle -> 创建订单+账单，跳转支付页
func (h *Pay) createOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUserOrRedirect(w, r)
	if !ok {
		return
	}
	productID, _ := strconv.ParseInt(r.PostFormValue("product_id"), 10, 64)
	cycle := r.PostFormValue("cycle")
	if cycle == "" { // 兼容表单直接提交
		cycle = "monthly"
	}
	psID, err := h.Products.DefaultPricesetID(r.Context())
	if err != nil {
		http.Error(w, "系统未配置价格组", 500)
		return
	}
	// 收集配置项选择：cfg_<field> -> value，服务端只认产品声明的 field
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", 400)
		return
	}
	selection := map[string]string{}
	for key, vals := range r.PostForm {
		if strings.HasPrefix(key, "cfg_") && len(vals) > 0 && vals[0] != "" {
			selection[strings.TrimPrefix(key, "cfg_")] = vals[0]
		}
	}
	coupon := strings.TrimSpace(r.PostFormValue("coupon"))
	orderID, invID, amount, err := h.Orders.CreateOrder(r.Context(), userID, productID, psID, cycle, selection, coupon)
	if err != nil {
		if errors.Is(err, service.ErrIdentityRequired) {
			http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 0 元订单（纯免费产品）：创建即自动核销并开通，跳过支付页。余额/真实网关都无法处理 0 金额。
	if amount == "0.00" {
		no, qerr := h.Invoices.NoByID(r.Context(), invID)
		if qerr != nil {
			log.Printf("[0元购] 读取账单号失败 invoice=%d: %v", invID, qerr)
			http.Error(w, "免费订单开通失败，请联系管理员", http.StatusInternalServerError)
			return
		}
		if perr := h.Payment.MarkPaid(r.Context(), no, "FREE-"+strconv.FormatInt(invID, 10), "balance"); perr != nil {
			log.Printf("[0元购] 订单 %d 自动核销失败: %v", orderID, perr)
			http.Error(w, "免费订单开通失败，请联系管理员", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/services", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(invID, 10), http.StatusSeeOther)
}

type payPageData struct {
	InvoiceNo   string
	Amount      string
	Status      string
	PayURL      string
	BalancePay  bool
	UserBalance string
	InvoiceID   string
	Error       string
	CSRF        string // 余额支付表单必填，否则会被 CSRF 中间件拦截
	Gateways    []payGatewayView
	Recharge    bool
	SiteName    string
	SiteMark    string
}

type payGatewayView struct {
	Code, Name, FeePercent, FeeAmount, Amount string
}

func quoteGateway(amount string, cfg map[string]string) (feePercent, feeAmount, payable string, err error) {
	feePercent, _, err = moneyutil.ParsePercent(cfg["fee_percent"])
	if err != nil {
		return "", "", "", err
	}
	feeAmount, payable, err = moneyutil.AddPercent(amount, feePercent)
	return feePercent, feeAmount, payable, err
}

func (h *Pay) payPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	no, amount, status, _, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	kind, _ := h.Invoices.KindByID(r.Context(), id)
	data := payPageData{InvoiceNo: no, Amount: amount, InvoiceID: r.PathValue("invoiceID"), CSRF: h.pageCSRF(w, r), Recharge: kind == "recharge"}
	si := h.currentSiteInfo()
	data.SiteName = si.Name
	data.SiteMark = siteFirstMark(si.Name)
	if bal, err := h.Balance.Get(r.Context(), userID); err == nil {
		data.UserBalance = bal
	}
	switch {
	case status == 1:
		data.Status = "已支付"
	default:
		if h.GwRepo != nil {
			if list, lerr := h.GwRepo.Enabled(r.Context()); lerr == nil {
				for _, v := range list {
					if _, ok := h.Gateways[v.Driver]; !ok {
						continue
					}
					feePercent, feeAmount, payable, qerr := quoteGateway(amount, v.Config)
					if qerr != nil {
						log.Printf("[payment] 网关 %s 手续费配置无效: %v", v.Code, qerr)
						continue
					}
					data.Gateways = append(data.Gateways, payGatewayView{Code: v.Code, Name: v.Name, FeePercent: feePercent, FeeAmount: feeAmount, Amount: payable})
				}
			}
		}
		data.BalancePay = !data.Recharge
	}
	h.payOnce.Do(func() {
		h.payTpl, h.payErr = template.ParseFS(payFS, "templates/pay.html")
	})
	if h.payErr != nil {
		http.Error(w, h.payErr.Error(), 500)
		return
	}
	if err := h.payTpl.Execute(w, data); err != nil {
		log.Printf("[template] pay.html 执行失败: %v", err)
	}
}

// start 绑定本次支付使用的网关实例并生成跳转地址。
func (h *Pay) start(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	no, amount, status, _, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || status != 0 || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	code := strings.TrimSpace(r.PostFormValue("gateway"))
	inst, err := h.GwRepo.Get(r.Context(), code)
	impl, ok := h.Gateways[inst.Driver]
	if err != nil || !inst.Enabled || !ok {
		http.Error(w, "支付网关不可用", http.StatusBadRequest)
		return
	}
	feePercent, feeAmount, payable, err := quoteGateway(amount, inst.Config)
	if err != nil {
		http.Error(w, "支付网关手续费配置无效", http.StatusBadRequest)
		return
	}
	attemptID, err := h.GwRepo.BindAttempt(r.Context(), id, code, payable, feePercent, feeAmount)
	if err != nil {
		http.Error(w, "创建支付记录失败", 500)
		return
	}
	base := siteBaseURL(r.Context(), h.Settings, r) // 站点地址：后台 site_url 优先，否则按请求推断
	notifyURL := base + "/pay/notify?" + url.Values{"code": {code}}.Encode()
	u, err := impl.PayURL(r.Context(), gateway.PayRequest{InvoiceNo: no, Amount: payable, Title: "LumeIDC 账单 " + no,
		NotifyURL: notifyURL, ReturnURL: base + "/pay/" + strconv.FormatInt(id, 10), Config: inst.Config})
	if err != nil {
		_ = h.GwRepo.MarkAttemptFailedByID(r.Context(), attemptID)
		log.Printf("[payment] 网关 %s 生成支付链接失败，账单 %s: %v", code, no, err)
		http.Error(w, "生成支付链接失败", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (h *Pay) ownsInvoice(r *http.Request, userID int64, no string) bool {
	owned, _ := h.Invoices.OwnedByNoUser(r.Context(), no, userID)
	return owned
}

// notify 由网关实例 code 分发到对应插件，账单核销保持统一。
func (h *Pay) notify(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" || strings.ContainsAny(code, "/?#&") {
		http.NotFound(w, r)
		return
	}
	log.Printf("[notify] 收到支付回调 code=%s 来源=%s", code, r.RemoteAddr)
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil {
		log.Printf("[notify] 网关 %s 不存在: %v", code, err)
		http.NotFound(w, r)
		return
	}
	impl, ok := h.Gateways[inst.Driver]
	if !ok {
		log.Printf("[notify] 网关 %s 驱动 %s 未注册", code, inst.Driver)
		http.NotFound(w, r)
		return
	}
	// 网关回调不是浏览器请求，但仍限制原始 body，避免无界表单解析。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		log.Printf("[notify] 网关 %s 表单解析失败: %v", code, err)
		w.Write([]byte("fail"))
		return
	}
	params := notifyParams(r)
	result, err := impl.VerifyNotify(params, inst.Config)
	if err != nil {
		log.Printf("[notify] 网关 %s 校验失败: %v (out_trade_no=%s trade_no=%s trade_status=%s) 参数=%s", code, err, params["out_trade_no"], params["trade_no"], params["trade_status"], notifyParamDump(params))
		w.Write([]byte("fail"))
		return
	}
	if !result.Successful {
		log.Printf("[notify] 网关 %s 交易未成功: out_trade_no=%s trade_status=%s", code, result.InvoiceNo, params["trade_status"])
		w.Write([]byte("success"))
		return
	}
	attempt, err := h.GwRepo.LatestAttempt(r.Context(), result.InvoiceNo, code, true)
	if err != nil || !equalAmount(result.Amount, attempt.Amount) {
		log.Printf("[notify] 网关 %s 账单/金额不匹配: invoice=%s 通知金额=%s 记录金额=%v err=%v", code, result.InvoiceNo, result.Amount, func() string {
			if err == nil {
				return attempt.Amount
			}
			return "无记录"
		}(), err)
		// A duplicate callback for the already completed attempt is harmless,
		// but it must still match the recorded trade and paid amount exactly.
		status, invoiceGateway, tradeNo, paidAmount, qerr := h.Invoices.RecordByNo(r.Context(), result.InvoiceNo)
		if qerr == nil && status == 1 &&
			invoiceGateway == code && tradeNo == result.TradeNo && equalAmount(result.Amount, paidAmount) {
			w.Write([]byte("success"))
			return
		}
		w.Write([]byte("fail"))
		return
	}
	if err := h.Payment.MarkPaid(r.Context(), result.InvoiceNo, result.TradeNo, code, attempt.ID); err != nil {
		if err != service.ErrAlreadyPaid {
			log.Printf("[notify] 网关 %s 核销失败 账单 %s: %v", code, result.InvoiceNo, err)
			w.Write([]byte("fail"))
			return
		}
		status, storedGateway, storedTrade, storedAmount, qerr := h.Invoices.RecordByNo(r.Context(), result.InvoiceNo)
		if qerr != nil || status != 1 ||
			storedGateway != code || storedTrade != result.TradeNo || !equalAmount(storedAmount, result.Amount) {
			w.Write([]byte("fail"))
			return
		}
	}
	log.Printf("[notify] 网关 %s 核销成功 账单 %s trade_no=%s 金额=%s", code, result.InvoiceNo, result.TradeNo, result.Amount)
	w.Write([]byte("success"))
}

// notifyParams 提取网关回调参数。r.ParseForm() 会把本站 URL 查询参数
// （如区分网关实例的 code）混入 r.Form，而网关签名内容不含这些参数，
// 若一并参与验签必然失败导致账单永不核销，故剔除；只保留网关回传的参数。
func notifyParams(r *http.Request) map[string]string {
	params := map[string]string{}
	for k, vals := range r.Form {
		if k == "code" || len(vals) == 0 {
			continue
		}
		params[k] = vals[0]
	}
	return params
}

// notifyParamDump 输出回调参数明细用于排查验签失败；sign 只保留前缀避免日志过长。
func notifyParamDump(params map[string]string) string {
	parts := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" && len(v) > 16 {
			v = v[:16] + "..."
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func equalAmount(a, b string) bool {
	_, x, errX := moneyutil.ParsePositive(a, 999999999999)
	_, y, errY := moneyutil.ParsePositive(b, 999999999999)
	return errX == nil && errY == nil && x == y
}

// payByBalance 余额支付账单。
func (h *Pay) payByBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	no, _, _, _, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	if err := h.Payment.MarkPaidByBalance(r.Context(), no, userID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}

// mockPayPage GET /mock/pay/{no} — 模拟支付确认页（测试网关）。仅本人可见。
func (h *Pay) mockPayPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	no := r.PathValue("no")
	_, code, amount, status, qerr := h.Invoices.MockByNoUser(r.Context(), no, userID, false)
	if qerr != nil || status != 0 || code == "" {
		http.NotFound(w, r)
		return
	}
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil || !inst.Enabled || inst.Driver != "mock" {
		http.NotFound(w, r)
		return
	}
	attempt, err := h.GwRepo.LatestAttempt(r.Context(), no, code, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cs := h.pageCSRF(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>模拟支付</title></head>
<body style="font-family:system-ui;padding:40px">
<h2>模拟支付（测试网关）</h2>
<p>账单号：%s</p><p>账单金额：¥%s</p><p>手续费：¥%s</p><p>应付金额：¥%s</p>
<form method="post" action="/mock/pay/%s">
<input type="hidden" name="_csrf" value="%s">
<button type="submit" style="padding:8px 20px">确认到账</button>
</form>
<p style="color:#888;font-size:13px">该网关仅用于测试，不会产生真实交易。</p>
</body></html>`, no, amount, attempt.FeeAmount, attempt.Amount, no, cs)
}

// mockConfirm POST /mock/pay/{no} — 确认模拟支付，核销账单（受全局 CSRF 中间件保护）。
func (h *Pay) mockConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	no := r.PathValue("no")
	_, code, _, status, qerr := h.Invoices.MockByNoUser(r.Context(), no, userID, true)
	if qerr != nil || status != 0 || code == "" {
		http.NotFound(w, r)
		return
	}
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil || !inst.Enabled || inst.Driver != "mock" {
		http.NotFound(w, r)
		return
	}
	attempt, err := h.GwRepo.LatestAttempt(r.Context(), no, code, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Payment.MarkPaid(r.Context(), no, "MOCK-"+strconv.FormatInt(time.Now().UnixNano(), 10), code, attempt.ID); err != nil && err != service.ErrAlreadyPaid {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}
