package handler

import (
	"embed"
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
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

//go:embed templates/pay.html
var payFS embed.FS

type Pay struct {
	Orders   *service.Orders
	Payment  *service.Payment
	Products *repo.Products
	Gateways map[string]gateway.Gateway
	BaseURL  string
	GwRepo   *repo.Gateways
	Balance  *repo.Balance
}

func (h *Pay) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /order", h.createOrder)
	mux.HandleFunc("GET /pay/notify/{code}", h.notify)
	mux.HandleFunc("POST /pay/{invoiceID}/start", h.start)
	mux.HandleFunc("POST /pay/{invoiceID}/balance", h.payByBalance)
	mux.HandleFunc("GET /pay/{invoiceID}", h.payPage)
	mux.HandleFunc("GET /mock/pay/{no}", h.mockPayPage)
	mux.HandleFunc("POST /mock/pay/{no}", h.mockConfirm)
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
	_, invID, _, err := h.Orders.CreateOrder(r.Context(), userID, productID, psID, cycle, selection, coupon)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
}

type payGatewayView struct{ Code, Name string }

func (h *Pay) loadInvoice(r *http.Request, invoiceID int64) (no string, amount string, status int16, gatewayCode string, err error) {
	row := h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT no,amount,status,gateway FROM invoices WHERE id=$1`, invoiceID)
	err = row.Scan(&no, &amount, &status, &gatewayCode)
	return
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
	no, amount, status, _, err := h.loadInvoice(r, id)
	if err != nil || !h.ownsInvoice(r, userID, no) {
		http.NotFound(w, r)
		return
	}
	var kind string
	_ = h.Payment.DB.QueryRowContext(r.Context(), `SELECT kind FROM invoices WHERE id=$1`, id).Scan(&kind)
	data := payPageData{InvoiceNo: no, Amount: amount, InvoiceID: r.PathValue("invoiceID"), CSRF: csrfOf(sessionsStore, w, r), Recharge: kind == "recharge"}
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
					if _, ok := h.Gateways[v.Driver]; ok {
						data.Gateways = append(data.Gateways, payGatewayView{Code: v.Code, Name: v.Name})
					}
				}
			}
		}
		data.BalancePay = !data.Recharge
	}
	tpl, err := template.ParseFS(payFS, "templates/pay.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tpl.Execute(w, data); err != nil {
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
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("invoiceID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	no, amount, status, _, err := h.loadInvoice(r, id)
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
	attemptID, err := h.GwRepo.BindAttempt(r.Context(), id, code, amount)
	if err != nil {
		http.Error(w, "创建支付记录失败", 500)
		return
	}
	u, err := impl.PayURL(r.Context(), gateway.PayRequest{InvoiceNo: no, Amount: amount, Title: "LumeIDC 账单 " + no,
		NotifyURL: h.BaseURL + "/pay/notify/" + url.PathEscape(code), ReturnURL: h.BaseURL + "/pay/" + strconv.FormatInt(id, 10), Config: inst.Config})
	if err != nil {
		_ = h.GwRepo.MarkAttemptFailedByID(r.Context(), attemptID)
		http.Error(w, "生成支付链接失败", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (h *Pay) ownsInvoice(r *http.Request, userID int64, no string) bool {
	var n int64
	h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT count(*) FROM invoices WHERE no=$1 AND user_id=$2`, no, userID).Scan(&n)
	return n == 1
}

// notify 由网关实例 code 分发到对应插件，账单核销保持统一。
func (h *Pay) notify(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	inst, err := h.GwRepo.Get(r.Context(), code)
	impl, ok := h.Gateways[inst.Driver]
	if err != nil || !inst.Enabled || !ok {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	params := map[string]string{}
	for k := range r.Form {
		params[k] = r.Form.Get(k)
	}
	result, err := impl.VerifyNotify(params, inst.Config)
	if err != nil {
		w.Write([]byte("fail"))
		return
	}
	if !result.Successful {
		w.Write([]byte("success"))
		return
	}
	var expected string
	if err := h.Payment.DB.QueryRowContext(r.Context(), `SELECT amount::text FROM invoices WHERE no=$1 AND gateway=$2`, result.InvoiceNo, code).Scan(&expected); err != nil || !equalAmount(result.Amount, expected) {
		w.Write([]byte("fail"))
		return
	}
	if err := h.Payment.MarkPaid(r.Context(), result.InvoiceNo, result.TradeNo, code); err != nil &&
		err != service.ErrAlreadyPaid {
		w.Write([]byte("fail"))
		return
	}
	w.Write([]byte("success"))
}

func equalAmount(a, b string) bool {
	parse := func(s string) (int64, bool) {
		parts := strings.SplitN(strings.TrimSpace(s), ".", 2)
		if len(parts) == 1 {
			parts = append(parts, "")
		}
		if len(parts[1]) > 2 {
			return 0, false
		}
		frac := parts[1] + strings.Repeat("0", 2-len(parts[1]))
		whole, err1 := strconv.ParseInt(parts[0], 10, 64)
		cents, err2 := strconv.ParseInt(frac, 10, 64)
		return whole*100 + cents, err1 == nil && err2 == nil && whole >= 0
	}
	x, okX := parse(a)
	y, okY := parse(b)
	return okX && okY && x == y
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
	no, _, _, _, err := h.loadInvoice(r, id)
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
	var invoiceID int64
	var amount, code string
	var status int16
	if err := h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT id,amount::text,status,gateway FROM invoices WHERE no=$1 AND user_id=$2`, no, userID).
		Scan(&invoiceID, &amount, &status, &code); err != nil || status != 0 || code == "" {
		http.NotFound(w, r)
		return
	}
	inst, err := h.GwRepo.Get(r.Context(), code)
	impl, exists := h.Gateways[inst.Driver]
	if err != nil || !inst.Enabled || !exists || inst.Driver != "mock" {
		http.NotFound(w, r)
		return
	}
	var pending bool
	if err := h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE invoice_id=$1 AND gateway_code=$2 AND status=0 AND amount=$3::numeric)`, invoiceID, code, amount).Scan(&pending); err != nil || !pending {
		http.NotFound(w, r)
		return
	}
	_ = impl
	csrf := csrfOf(sessionsStore, w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>模拟支付</title></head>
<body style="font-family:system-ui;padding:40px">
<h2>模拟支付（测试网关）</h2>
<p>账单号：%s</p><p>金额：¥%s</p>
<form method="post" action="/mock/pay/%s">
<input type="hidden" name="_csrf" value="%s">
<button type="submit" style="padding:8px 20px">确认到账</button>
</form>
<p style="color:#888;font-size:13px">该网关仅用于测试，不会产生真实交易。</p>
</body></html>`, no, amount, no, csrf)
}

// mockConfirm POST /mock/pay/{no} — 确认模拟支付，核销账单（受全局 CSRF 中间件保护）。
func (h *Pay) mockConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	no := r.PathValue("no")
	var invoiceID int64
	var code, amount string
	var status int16
	if err := h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT id,gateway,amount::text,status FROM invoices WHERE no=$1 AND user_id=$2 FOR SHARE`, no, userID).
		Scan(&invoiceID, &code, &amount, &status); err != nil || status != 0 || code == "" {
		http.NotFound(w, r)
		return
	}
	inst, err := h.GwRepo.Get(r.Context(), code)
	if err != nil || !inst.Enabled || inst.Driver != "mock" {
		http.NotFound(w, r)
		return
	}
	var pending bool
	if err := h.Payment.DB.QueryRowContext(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE invoice_id=$1 AND gateway_code=$2 AND status=0 AND amount=$3::numeric)`, invoiceID, code, amount).Scan(&pending); err != nil || !pending {
		http.NotFound(w, r)
		return
	}
	if err := h.Payment.MarkPaid(r.Context(), no, "MOCK-"+strconv.FormatInt(time.Now().UnixNano(), 10), code); err != nil && err != service.ErrAlreadyPaid {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}
