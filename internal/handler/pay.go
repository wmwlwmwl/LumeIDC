package handler

import (
	"context"
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
	"time"

	"lumeidc/internal/gateway"
	"lumeidc/internal/middleware"
	moneyutil "lumeidc/internal/money"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"

	qrcode "github.com/skip2/go-qrcode"
)

type Pay struct {
	Orders   *service.Orders
	Payment  *service.Payment
	Products *repo.Products
	Gateways map[string]gateway.Gateway
	GwRepo   *repo.Gateways
	Invoices *repo.Invoices
	Balance  *repo.Balance
	*Deps
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
	mux.HandleFunc("GET "+gateway.LocalCheckoutPath, h.localCheckoutPage)
	mux.HandleFunc("GET /mock/pay/{no}", h.mockPayPage)
	mux.HandleFunc("POST /mock/pay/{no}", h.mockConfirm)
}

// localCheckoutPage 渲染本地结算页（如扫码支付二维码）。由实现
// gateway.LocalCheckout 能力的网关驱动，避免在通用处理器里写死支付品牌。
func (h *Pay) localCheckoutPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	no := strings.TrimSpace(r.URL.Query().Get("invoice"))
	qrText := r.URL.Query().Get("data")
	if no == "" || qrText == "" {
		http.NotFound(w, r)
		return
	}
	id, status, driver, gatewayCode, baseAmount, qerr := h.Invoices.CheckoutByNoUser(r.Context(), no, userID)
	if qerr != nil || status != 0 {
		http.NotFound(w, r)
		return
	}
	impl, ok := h.Gateways[driver]
	if _, isLocal := impl.(gateway.LocalCheckout); !ok || !isLocal {
		http.NotFound(w, r)
		return
	}
	brand := impl.Name()
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
	if _, err := fmt.Fprintf(w, qrPageHTML, artStyleTag,
		template.HTMLEscapeString(brand), attempt.Amount, template.HTMLEscapeString(no), baseAmount, dataURI, id, id, id); err != nil {
		log.Printf("[template] 二维码结算页输出失败: %v", err)
	}
}

// qrPageHTML 本地扫码结算页；artStyleTag 对齐 Art Design Pro（templates/artpage.css），
// 样式含系统暗色跟随。参数：1 样式 2 品牌(已转义) 3 应付金额 4 账单号(已转义)
// 5 账单金额 6 二维码 dataURI 7 账单 ID（结算页链接与轮询）。
const qrPageHTML = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%[2]s扫码支付</title>%[1]s</head>
<body>
<main class="art-card">
  <div class="art-card__body">
    <div class="art-brand"><span class="art-brand__name">%[2]s</span><small>扫码支付</small></div>
    <p class="art-amount">￥%[3]s</p>
    <div class="art-meta">
      <div class="art-meta__row"><span>账单号</span><b>%[4]s</b></div>
      <div class="art-meta__row"><span>账单金额</span><b>￥%[5]s</b></div>
    </div>
    <img class="art-qr" src="%[6]s" alt="支付二维码">
    <p class="art-muted" id="message" role="status">支付完成后页面会自动检查到账状态</p>
    <div class="art-actions"><a class="art-btn art-btn--outline" href="/pay/%[7]d">返回账单页</a></div>
  </div>
</main>
<script>(function(){var message=document.getElementById('message');function check(){fetch('/pay/%[7]d/status',{credentials:'same-origin',headers:{'Accept':'application/json'}}).then(function(response){if(!response.ok)throw new Error();return response.json()}).then(function(data){if(data.paid){message.textContent='支付成功，正在返回账单页';location.href='/pay/%[7]d';return}if(data.expired){message.textContent='账单已过期，请返回重新下单';return}setTimeout(check,4000)}).catch(function(){message.textContent='状态检查失败，正在重试';setTimeout(check,5000)})}check()})();</script>
</body>
</html>`

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
	json.NewEncoder(w).Encode(map[string]bool{"paid": status == 1, "expired": status == 3})
}

// createOrder POST product_id & cycle -> 创建订单+账单，跳转支付页
func (h *Pay) createOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	vals, err := bodyValues(r)
	if err != nil {
		jsonStatus(w, r, 400, "表单解析失败")
		return
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	productID, _ := strconv.ParseInt(fv("product_id"), 10, 64)
	cycle := fv("cycle")
	if cycle == "" { // 兼容表单直接提交
		cycle = "monthly"
	}
	psID, err := h.Products.DefaultPricesetID(r.Context())
	if err != nil {
		jsonStatus(w, r, 500, "系统未配置价格组")
		return
	}
	// 收集配置项选择：cfg_<field> -> value，服务端只认产品声明的 field
	selection := map[string]string{}
	if vals != nil {
		for k, v := range vals {
			if strings.HasPrefix(k, "cfg_") && v != "" {
				selection[strings.TrimPrefix(k, "cfg_")] = v
			}
		}
	} else {
		if err := r.ParseForm(); err != nil {
			jsonStatus(w, r, 400, "表单解析失败")
			return
		}
		for key, vs := range r.PostForm {
			if strings.HasPrefix(key, "cfg_") && len(vs) > 0 && vs[0] != "" {
				selection[strings.TrimPrefix(key, "cfg_")] = vs[0]
			}
		}
	}
	coupon := strings.TrimSpace(fv("coupon"))
	orderID, invID, amount, err := h.Orders.CreateOrder(r.Context(), userID, productID, psID, cycle, selection, coupon)
	if err != nil {
		if errors.Is(err, service.ErrIdentityRequired) {
			if wantsJSON(r) {
				writeJSON(w, map[string]any{"ok": 0, "code": "identity_required", "msg": err.Error(), "redirect": "/user/verification"})
				return
			}
			http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		// 上游价格已变（本地已同步）：前端据此重载购买页，让用户以新价重新确认。
		// 用 400 而非 200：前端 http 层只在非 2xx 时才抛错，200+ok:0 会被当成下单成功。
		if errors.Is(err, service.ErrUpstreamPriceChanged) {
			if wantsJSON(r) {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"ok": 0, "code": "price_changed", "msg": err.Error()})
				return
			}
			http.Redirect(w, r, "/buy/"+strconv.FormatInt(productID, 10), http.StatusSeeOther)
			return
		}
		// 上游已下架该商品：前端提示后回到产品中心。
		if errors.Is(err, service.ErrUpstreamUnshelved) {
			if wantsJSON(r) {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"ok": 0, "code": "unshelved", "msg": err.Error()})
				return
			}
			http.Redirect(w, r, "/cart", http.StatusSeeOther)
			return
		}
		jsonStatus(w, r, 400, err.Error())
		return
	}
	// 0 元订单（纯免费产品）：创建即自动核销并开通，跳过支付页。余额/真实网关都无法处理 0 金额。
	if amount == "0.00" {
		no, qerr := h.Invoices.NoByID(r.Context(), invID)
		if qerr != nil {
			log.Printf("[0元购] 读取账单号失败 invoice=%d: %v", invID, qerr)
			jsonStatus(w, r, 500, "免费订单开通失败，请联系管理员")
			return
		}
		if perr := h.Payment.MarkPaid(r.Context(), no, "FREE-"+strconv.FormatInt(invID, 10), "balance"); perr != nil {
			log.Printf("[0元购] 订单 %d 自动核销失败: %v", orderID, perr)
			jsonStatus(w, r, 500, "免费订单开通失败，请联系管理员")
			return
		}
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "paid": true, "redirect": "/services"})
			return
		}
		http.Redirect(w, r, "/services", http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "paid": false, "invoice_id": invID, "amount": amount, "redirect": "/pay/" + strconv.FormatInt(invID, 10)})
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
	no, amount, status, _, credit, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	// 易支付页面跳转通知会把支付参数追加到 return_url。异步通知仍是
	// 首选，但处理浏览器回跳可以覆盖内网环境无法接收异步通知的情况。
	if r.URL.Query().Get("out_trade_no") != "" {
		if status != 1 {
			if err := h.settleReturn(r, no); err != nil {
				log.Printf("[return] 支付回跳核销失败 invoice=%s: %v", no, err)
			}
		}
		// 无论核销成功与否都跳回干净的收银台页：失败时由异步通知/补单兜底，
		// 绝不把 payPage 的 JSON 直接展示在浏览器地址栏。
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
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
	// 在线支付只针对“账单金额 - 已抵扣余额”，手续费也只按该剩余额计算。
	remaining := amount
	if _, aCents, aerr := moneyutil.ParsePositive(amount, 999999999999); aerr == nil {
		cCents := int64(0)
		if _, c, cerr := moneyutil.ParseNonNegative(credit, 999999999999); cerr == nil {
			cCents = c
		}
		if cCents > aCents {
			cCents = aCents
		}
		remaining = moneyutil.FormatCents(aCents - cCents)
	}
	switch {
	case status == 1:
		data.Status = "已支付"
	case status == 3:
		data.Status = "已过期"
	default:
		if h.GwRepo != nil {
			if list, lerr := h.GwRepo.Enabled(r.Context()); lerr == nil {
				for _, v := range list {
					if _, ok := h.Gateways[v.Driver]; !ok {
						continue
					}
					feePercent, feeAmount, payable, qerr := quoteGateway(remaining, v.Config)
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
	gateways := make([]map[string]any, 0, len(data.Gateways))
	for _, g := range data.Gateways {
		gateways = append(gateways, map[string]any{
			"code": g.Code, "name": g.Name, "fee_percent": g.FeePercent,
			"fee_amount": g.FeeAmount, "amount": g.Amount,
		})
	}
	writeJSON(w, map[string]any{
		"ok": 1,
		"invoice": map[string]any{
			"id": data.InvoiceID, "no": data.InvoiceNo, "amount": data.Amount,
			"credit": credit, "remaining": remaining,
			"status": data.Status, "recharge": data.Recharge,
		},
		"paid":        data.Status == "已支付",
		"expired":     data.Status == "已过期",
		"balance_pay": data.BalancePay && data.Status != "已支付",
		"balance":     data.UserBalance,
		"gateways":    gateways,
		"csrf":        data.CSRF,
		"site_name":   data.SiteName,
	})
}

// settleReturn verifies and settles a signed browser return from a gateway.
// It deliberately uses the same pending payment attempt checks as async notify.
func (h *Pay) settleReturn(r *http.Request, invoiceNo string) error {
	params := make(map[string]string, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		if key == "code" || len(values) == 0 {
			continue
		}
		params[key] = values[0]
	}
	code, err := h.GwRepo.InvoiceGateway(r.Context(), invoiceNo)
	if err != nil {
		return err
	}
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil {
		return err
	}
	// 停用即停收：与下单、模拟支付、补单（PendingPaymentAttempts 过滤 enabled）一致，
	// 避免管理员因密钥泄露停用网关后，泄露的密钥仍能核销账单。
	if !inst.Enabled {
		return fmt.Errorf("支付网关已停用")
	}
	impl, ok := h.Gateways[inst.Driver]
	if !ok {
		return fmt.Errorf("支付网关驱动未注册")
	}
	result, err := impl.VerifyNotify(params, inst.Config)
	if err != nil {
		return err
	}
	if !result.Successful || result.InvoiceNo != invoiceNo {
		return fmt.Errorf("支付回跳状态未成功")
	}
	return h.applyGatewayPayment(r.Context(), result, code)
}

// applyGatewayPayment 统一处理一笔已验签成功的到账：
//   - 匹配当前绑定且待支付的尝试 → 核销账单并开通；
//   - 只能匹配到历史/失效尝试（切换网关后的迟到回调）→ 到账金额退回余额；
//   - 账单已支付且与本流水一致 → 视为重复回调；
//   - 账单已支付但为另一次到账（重复支付）→ 到账金额退回余额。
//
// 退回余额以支付尝试的 provider_trade_no 幂等，重复回调不会重复入账。
func (h *Pay) applyGatewayPayment(ctx context.Context, result gateway.NotifyResult, code string) error {
	if attempt, err := h.GwRepo.LatestAttempt(ctx, result.InvoiceNo, code, true); err == nil && equalAmount(result.Amount, attempt.Amount) {
		if merr := h.Payment.MarkPaid(ctx, result.InvoiceNo, result.TradeNo, code, attempt.ID); merr != nil && merr != service.ErrAlreadyPaid {
			return merr
		}
		return nil
	}
	// 无法核销当前待支付尝试：查该网关金额一致的历史尝试，退回余额。
	// 必须按金额找而不只看最新一条：同一账单重开支付会把旧尝试置为失效，
	// 但旧二维码/链接在网关侧仍可付，迟到回调的金额只对得上那条旧尝试。
	if stale, err := h.GwRepo.AttemptByInvoiceGatewayAmount(ctx, result.InvoiceNo, code, result.Amount); err == nil {
		if cerr := h.Payment.CreditCapturedToBalance(ctx, stale.ID, result.TradeNo); cerr != nil {
			return cerr
		}
		return nil
	}
	// 重复回调：账单已支付且与本流水完全一致。
	status, storedGateway, storedTrade, storedAmount, qerr := h.Invoices.RecordByNo(ctx, result.InvoiceNo)
	if qerr == nil && status == 1 && storedGateway == code &&
		storedTrade == result.TradeNo && equalAmount(storedAmount, result.Amount) {
		return nil
	}
	return fmt.Errorf("支付记录/金额不匹配")
}

// start 绑定本次支付使用的网关实例并生成跳转地址。
func (h *Pay) start(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	// SPA 由 CSRF 中间件按 X-CSRF-Token 头校验；SSR 表单手动校验 _csrf。
	if vals == nil {
		tok := fv("_csrf")
		sess := middleware.FromSession(r.Context())
		if tok == "" || sess == nil || tok != sess.CSRFToken() {
			middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
			return
		}
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	no, _, status, _, _, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || status != 0 || !h.ownsInvoice(r, userID, no) {
		if status == 3 && h.ownsInvoice(r, userID, no) {
			jsonStatus(w, r, http.StatusBadRequest, "账单已过期")
			return
		}
		http.NotFound(w, r)
		return
	}
	code := strings.TrimSpace(fv("gateway"))
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil {
		jsonStatus(w, r, http.StatusBadRequest, "支付网关不可用")
		return
	}
	impl, ok := h.Gateways[inst.Driver]
	if !inst.Enabled || !ok {
		jsonStatus(w, r, http.StatusBadRequest, "支付网关不可用")
		return
	}
	feePercent, _, err := moneyutil.ParsePercent(inst.Config["fee_percent"])
	if err != nil {
		jsonStatus(w, r, http.StatusBadRequest, "支付网关手续费配置无效")
		return
	}
	// 组合支付：按需先用余额抵扣，再就剩余本金走在线支付（手续费只对在线本金收取）。
	// 付款前复核：下单后上游改价（同步已落到本地）时先拦下，用户还没花钱就能重新下单。
	if verr := h.Payment.VerifyOrderPriceBeforePay(r.Context(), id, userID); verr != nil {
		jsonStatus(w, r, http.StatusBadRequest, verr.Error())
		return
	}
	prep, err := h.Payment.PrepareOnline(r.Context(), id, userID, code, feePercent, fv("use_balance") == "1")
	if err != nil {
		jsonStatus(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if prep.FullyCovered {
		if perr := h.Payment.MarkPaidByBalance(r.Context(), no, userID); perr != nil {
			log.Printf("[payment] 账单 %s 余额全额抵扣核销失败: %v", no, perr)
			jsonStatus(w, r, http.StatusBadRequest, perr.Error())
			return
		}
		redirect := "/pay/" + strconv.FormatInt(id, 10)
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "paid": true, "redirect": redirect})
			return
		}
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	base := siteBaseURL(r.Context(), h.Settings, r) // 站点地址：后台 site_url 优先，否则按请求推断
	notifyURL := base + "/pay/notify?" + url.Values{"code": {code}}.Encode()
	u, err := impl.PayURL(r.Context(), gateway.PayRequest{InvoiceNo: no, Amount: prep.Payable, Title: h.currentSiteInfo().Name + " 账单 " + no,
		NotifyURL: notifyURL, ReturnURL: base + "/pay/" + strconv.FormatInt(id, 10), Config: inst.Config})
	if err != nil {
		_ = h.Payment.ReleaseInvoiceCredit(r.Context(), id, prep.AttemptID)
		log.Printf("[payment] 网关 %s 生成支付链接失败，账单 %s: %v", code, no, err)
		jsonStatus(w, r, http.StatusBadGateway, "生成支付链接失败")
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "url": u})
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
	if code == "" {
		// 兜底：部分网关（如支付宝后台配置固定异步通知地址）回调不带 code。
		// 账单支付时已绑定网关实例（invoices.gateway），按 out_trade_no 反查。
		no := strings.TrimSpace(r.PostFormValue("out_trade_no"))
		if no != "" {
			if c, err := h.GwRepo.InvoiceGateway(r.Context(), no); err == nil && c != "" {
				code = c
			}
		}
	}
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
	// 停用即停收（与下单、模拟支付、补单一致）：否则管理员为密钥泄露等原因
	// 停用网关后，泄露的密钥仍能伪造回调把账单核销掉。
	if !inst.Enabled {
		log.Printf("[notify] 网关 %s 已停用，忽略回调", code)
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
	if err := h.applyGatewayPayment(r.Context(), result, code); err != nil {
		log.Printf("[notify] 网关 %s 处理到账失败 账单 %s trade_no=%s 金额=%s: %v", code, result.InvoiceNo, result.TradeNo, result.Amount, err)
		w.Write([]byte("fail"))
		return
	}
	log.Printf("[notify] 网关 %s 处理到账成功 账单 %s trade_no=%s 金额=%s", code, result.InvoiceNo, result.TradeNo, result.Amount)
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
	no, _, _, _, _, err := h.Invoices.LoadByID(r.Context(), id)
	if err != nil || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	// 付款前复核：下单后上游改价（同步已落到本地）时先拦下，钱还没花就让用户重新下单
	if verr := h.Payment.VerifyOrderPriceBeforePay(r.Context(), id, userID); verr != nil {
		jsonStatus(w, r, http.StatusBadRequest, verr.Error())
		return
	}
	if err := h.Payment.MarkPaidByBalance(r.Context(), no, userID); err != nil {
		jsonStatus(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "余额支付成功", "redirect": "/services"})
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
	escNo := template.HTMLEscapeString(no)
	fmt.Fprintf(w, mockPayPageHTML, artStyleTag, escNo, amount, attempt.FeeAmount, attempt.Amount, cs)
}

// mockPayPageHTML 模拟支付确认页（测试网关）。参数：1 样式 2 账单号(已转义)
// 3 账单金额 4 手续费 5 应付金额 6 CSRF 令牌。
const mockPayPageHTML = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>模拟支付</title>%[1]s</head>
<body>
<main class="art-card">
  <div class="art-card__body">
    <div class="art-result">
      <h1>模拟支付</h1>
      <p>测试网关，仅用于联调，不会产生真实交易。</p>
    </div>
    <div class="art-meta">
      <div class="art-meta__row"><span>账单号</span><b>%[2]s</b></div>
      <div class="art-meta__row"><span>账单金额</span><b>￥%[3]s</b></div>
      <div class="art-meta__row"><span>手续费</span><b>￥%[4]s</b></div>
      <div class="art-meta__row"><span>应付金额</span><b>￥%[5]s</b></div>
    </div>
    <form method="post" action="/mock/pay/%[2]s">
      <input type="hidden" name="_csrf" value="%[6]s">
      <div class="art-actions"><button type="submit" class="art-btn art-btn--primary">确认到账</button></div>
    </form>
  </div>
</main>
</body>
</html>`

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
