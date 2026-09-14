package zjmf

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"lumeidc/internal/server"
)

// memCheckpoint 内存版 CheckpointStore，供开通流程自检使用。
type memCheckpoint struct{ m map[string]string }

func newMemCheckpoint() *memCheckpoint { return &memCheckpoint{m: map[string]string{}} }

func (c *memCheckpoint) GetCheckpoint(k string) (string, bool, error) {
	v, ok := c.m[k]
	return v, ok, nil
}
func (c *memCheckpoint) SetCheckpoint(k, v string) error { c.m[k] = v; return nil }
func (c *memCheckpoint) DeleteCheckpoint(k string) error { delete(c.m, k); return nil }

// invoiceResp 构造 /get_invoices_detail 的响应体；total 原样嵌入，便于测字符串/数字两种上游形态。
// 真实未付账单带 status=Unpaid；状态缺失是另一种异常形态（见 invoice_gone_test）。
func invoiceResp(total string) string {
	return `{"status":200,"data":{"detail":{"status":"Unpaid","total":` + total + `}}}`
}

// provisionEnv 起一个能走完「清空购物车 → 取配置 → 加购 → 结算」的假上游。
// invoiceBody 是 /get_invoices_detail 的响应体（可换成错误响应）；payCalled 记录是否付了款。
func provisionEnv(t *testing.T, invoiceBody string, payCalled *bool) server.Config {
	t.Helper()
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001","hostids":[555]}}`))
		case "/get_invoices_detail":
			w.Write([]byte(invoiceBody))
		case "/apply_credit":
			*payCalled = true
			w.Write([]byte(`{"status":200,"data":{"hostid":[555]}}`))
		default:
			http.NotFound(w, r)
		}
	})
}

// provisionEnvPayFail 同 provisionEnv，但 /apply_credit（余额付款）失败，用于验证失败分流。
// settle 不返回 hostid —— 对应"上游余额不足所以没开通"（auto_setup=payment 在付款时才建 host）。
func provisionEnvPayFail(t *testing.T, invoiceBody string, payCalled *bool) server.Config {
	t.Helper()
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001"}}`))
		case "/get_invoices_detail":
			w.Write([]byte(invoiceBody))
		case "/apply_credit":
			*payCalled = true
			w.Write([]byte(`{"status":400,"msg":"账户余额不足"}`))
		default:
			http.NotFound(w, r)
		}
	})
}

// provisionOnceWith 与 provisionOnce 同，但可指定假上游构造器。
func provisionOnceWith(t *testing.T, cfg server.Config, ck *memCheckpoint, expectAmount float64) error {
	t.Helper()
	var p Provider
	_, err := p.Provision(context.Background(), cfg, server.ProvisionRequest{
		UpstreamPID: 1001, Cycle: "monthly", ServiceID: 7, ExpectAmount: expectAmount,
	}, ck)
	return err
}

// provisionOnce 跑一次开通，接受上游账单响应与下单时成本，返回错误、是否已付款。
func provisionOnce(t *testing.T, invoiceBody string, ck *memCheckpoint, expectAmount float64) (error, bool) {
	t.Helper()
	paid := false
	cfg := provisionEnv(t, invoiceBody, &paid)
	return provisionOnceWith(t, cfg, ck, expectAmount), paid
}

// 上游按当前价出的账单高于下单时成本 → 停在未付款，返回 PriceChangedError 交人工二选一。
func TestProvisionStopsOnPriceIncrease(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"12.00"`), ck, 10)

	var pce *server.PriceChangedError
	if !errors.As(err, &pce) {
		t.Fatalf("上游涨价应返回 PriceChangedError，实得: %v", err)
	}
	if pce.UpstreamAmount != 12 || pce.ExpectAmount != 10 {
		t.Fatalf("金额解析错误: 上游 %v 期望 %v", pce.UpstreamAmount, pce.ExpectAmount)
	}
	if pce.UpstreamInvoiceID != "9001" || pce.UpstreamPID != 1001 {
		t.Fatalf("账单号/商品号未带回: %+v", pce)
	}
	if paid {
		t.Fatal("价格不一致时不得付款")
	}
	if !server.IsManualReview(err) {
		t.Fatal("应停止自动重试（标记人工处理）")
	}
	// 账单号已落检查点：管理员重试时直接付这张，跳过重建与比价。
	if got, ok, _ := ck.GetCheckpoint(ckInvoice); !ok || got != "9001" {
		t.Fatalf("账单检查点应为 9001，实得 %q", got)
	}
}

// 上游价格与下单时一致 → 正常付款开通。
func TestProvisionPaysWhenPriceUnchanged(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"10.00"`), ck, 10)
	if err != nil {
		t.Fatalf("价格一致不应报错: %v", err)
	}
	if !paid {
		t.Fatal("价格一致时应付款")
	}
}

// 人工确认后重试：账单号已在检查点里 → 跳过比价直接付款（即"强制开通"）。
func TestProvisionRetryPaysExistingInvoice(t *testing.T) {
	ck := newMemCheckpoint()
	ck.m[ckInvoice] = "9001"
	// 账单 12 远高于期望 10，但沿用已有账单时不再比价。
	err, paid := provisionOnce(t, invoiceResp(`"12.00"`), ck, 10)
	if err != nil {
		t.Fatalf("沿用已有账单不应报错: %v", err)
	}
	if !paid {
		t.Fatal("沿用已有账单应直接付款（强制开通）")
	}
}

// 容差内（涨价 1 分）不拦：避免上游四舍五入口径差异导致正常订单被卡住。
func TestProvisionAllowsDifferenceWithinTolerance(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"12.01"`), ck, 12)
	if err != nil {
		t.Fatalf("涨价 0.01 在容差内不应报错: %v", err)
	}
	if !paid {
		t.Fatal("涨价 0.01 在容差内应正常付款")
	}
}

// 超出容差（涨价 3 分）即拦下。
func TestProvisionBlocksDifferenceOverTolerance(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"12.03"`), ck, 12)
	var pce *server.PriceChangedError
	if !errors.As(err, &pce) {
		t.Fatalf("涨价 0.03 超容差应拦下，实得: %v", err)
	}
	if paid {
		t.Fatal("超容差不得付款")
	}
}

// 上游降价：我们赚更多，不拦。
func TestProvisionAllowsPriceDrop(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"9.00"`), ck, 10)
	if err != nil {
		t.Fatalf("上游降价不应报错: %v", err)
	}
	if !paid {
		t.Fatal("上游降价应正常付款")
	}
}

// 老数据无下单快照（ExpectAmount=0）→ 无从比对，跳过并放行。
func TestProvisionSkipsCheckWithoutExpectAmount(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, invoiceResp(`"99.00"`), ck, 0)
	if err != nil {
		t.Fatalf("无比价基准时不应报错: %v", err)
	}
	if !paid {
		t.Fatal("无比价基准时应正常付款")
	}
}

// 读不到账单金额（上游报错）→ 无法判定价格，转人工复核而不是当涨价处理。
func TestProvisionManualReviewWhenInvoiceUnreadable(t *testing.T) {
	ck := newMemCheckpoint()
	err, paid := provisionOnce(t, `{"status":400,"msg":"账单不存在"}`, ck, 10)

	var mre *server.ManualReviewError
	if !errors.As(err, &mre) {
		t.Fatalf("读账单失败应返回 ManualReviewError，实得: %v", err)
	}
	var pce *server.PriceChangedError
	if errors.As(err, &pce) {
		t.Fatal("读账单失败不得误判为价格变动")
	}
	if paid {
		t.Fatal("无法核对价格时不得付款")
	}
	if !server.IsManualReview(err) {
		t.Fatal("应停止自动重试（标记人工处理）")
	}
}

// 上游余额不足：账单还在 → 保持重试（不转人工、不消耗次数），充值到账后自动付掉。
func TestProvisionRetriesLaterOnInsufficientBalance(t *testing.T) {
	paid := false
	ck := newMemCheckpoint()
	err := provisionOnceWith(t, provisionEnvPayFail(t, invoiceResp(`"10.00"`), &paid), ck, 10)

	var rle *server.RetryLaterError
	if !errors.As(err, &rle) {
		t.Fatalf("余额不足应返回 RetryLaterError，实得: %v", err)
	}
	if server.IsManualReview(err) {
		t.Fatal("余额不足不应转人工——充值是暂时的，应继续自动重试")
	}
	if !server.IsRetryLater(err) {
		t.Fatal("应被识别为保持重试类型")
	}
	if !paid {
		t.Fatal("应尝试过付款")
	}
	// 账单号仍在检查点：下次重试直接付这张，不会重复下单。
	if got, ok, _ := ck.GetCheckpoint(ckInvoice); !ok || got != "9001" {
		t.Fatalf("账单检查点应保留 9001，实得 %q", got)
	}
}

// 付款失败且上游账单已被删除 → 转人工：重试永远付不掉这张单。
func TestProvisionManualReviewWhenInvoiceGoneOnPayFail(t *testing.T) {
	paid := false
	ck := newMemCheckpoint()
	err := provisionOnceWith(t, provisionEnvPayFailGoneAfter(t, &paid), ck, 10)

	var mre *server.ManualReviewError
	if !errors.As(err, &mre) {
		t.Fatalf("账单已不存在应转人工，实得: %v", err)
	}
	if server.IsRetryLater(err) {
		t.Fatal("账单已消失不应保持重试")
	}
	if !paid {
		t.Fatal("应尝试过付款")
	}
}

// provisionEnvPayFailGoneAfter 付款失败，且账单在"比价通过之后、失败探测之前"被删除。
// 首次 /get_invoices_detail（比价）返回正常金额，之后（付款失败的探测）返回账单不存在。
func provisionEnvPayFailGoneAfter(t *testing.T, payCalled *bool) server.Config {
	t.Helper()
	var detailCalls atomic.Int32
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001"}}`))
		case "/get_invoices_detail":
			if detailCalls.Add(1) == 1 {
				w.Write([]byte(invoiceResp(`"10.00"`)))
				return
			}
			w.Write([]byte(`{"status":400,"msg":"账单不存在"}`))
		case "/apply_credit":
			*payCalled = true
			w.Write([]byte(`{"status":400,"msg":"账户余额不足"}`))
		default:
			http.NotFound(w, r)
		}
	})
}

// provisionEnvOrderHost 模拟 auto_setup=order：settle 阶段就返回 hostid。
// 付款第 1 次失败、之后成功。settleCalls/payCalls 用于断言重试没有重复下单。
func provisionEnvOrderHost(t *testing.T, invoiceBody string, settleCalls, payCalls *atomic.Int32) server.Config {
	t.Helper()
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			settleCalls.Add(1)
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001","hostids":[555]}}`))
		case "/get_invoices_detail":
			w.Write([]byte(invoiceBody))
		case "/apply_credit":
			if payCalls.Add(1) == 1 {
				w.Write([]byte(`{"status":400,"msg":"账户余额不足"}`))
				return
			}
			w.Write([]byte(`{"status":200,"data":{"hostid":[555]}}`))
		default:
			http.NotFound(w, r)
		}
	})
}

// 结算阶段就已建 host（auto_setup=order）+ 付款失败：必须保持重试。
// 回归防线：ckHosts 若在结算时就写，重试会被 checkpoint 1 短路成"成功"，
// 留下一张未付的上游账单（本地显示已开通、上游欠费停机），且任务标 succeeded 不再重试。
func TestProvisionKeepsRetryingWhenHostCreatedBeforePayment(t *testing.T) {
	var settleCalls, payCalls atomic.Int32
	cfg := provisionEnvOrderHost(t, invoiceResp(`"10.00"`), &settleCalls, &payCalls)
	ck := newMemCheckpoint()

	// 第一次：结算建了 host，但付款失败。
	if err := provisionOnceWith(t, cfg, ck, 10); !server.IsRetryLater(err) {
		t.Fatalf("付款失败应保持重试，实得: %v", err)
	}
	if _, ok, _ := ck.GetCheckpoint(ckHosts); ok {
		t.Fatal("付款成功前不得写 ckHosts——checkpoint 1 会据此短路返回成功，留下未付账单")
	}
	if got, ok, _ := ck.GetCheckpoint(ckInvoice); !ok || got != "9001" {
		t.Fatalf("应保留账单检查点供重试复用，实得 %q", got)
	}

	// 第二次（重试）：复用同一张账单重新付款，这次成功。
	var p Provider
	res, err := p.Provision(context.Background(), cfg, server.ProvisionRequest{
		UpstreamPID: 1001, Cycle: "monthly", ServiceID: 7, ExpectAmount: 10,
	}, ck)
	if err != nil {
		t.Fatalf("重试应成功: %v", err)
	}
	if res.UpstreamHostID != 555 {
		t.Fatalf("应返回 host 555，实得 %d", res.UpstreamHostID)
	}
	if payCalls.Load() != 2 {
		t.Fatalf("重试应重新发起付款（共 2 次），实得 %d", payCalls.Load())
	}
	if settleCalls.Load() != 1 {
		t.Fatalf("重试不应重复结算建单，实得 %d 次", settleCalls.Load())
	}
	if got, ok, _ := ck.GetCheckpoint(ckHosts); !ok || got != "555" {
		t.Fatalf("付款成功后应写 ckHosts=555，实得 %q", got)
	}
}

// 上游账单金额可能是字符串或数字（flexString），两种都要能解析。
func TestFetchUpstreamInvoiceAmountForms(t *testing.T) {
	for name, body := range map[string]string{
		"字符串": invoiceResp(`"12.50"`),
		"数字":  invoiceResp(`12.5`),
	} {
		t.Run(name, func(t *testing.T) {
			paid := false
			cfg := provisionEnv(t, body, &paid)
			amt, err := fetchUpstreamInvoiceAmount(context.Background(), cfg, "9001")
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if amt != 12.5 {
				t.Fatalf("金额应为 12.5，实得 %v", amt)
			}
		})
	}
}

// 上游账单已失效（被删除/作废）时自动重新结算并付款，不需要人工先「重置检查点」再重试。
// 一轮调用内完成：付旧账单失败 → 探测到已作废 → 清检查点 → 重新结算（比价）→ 付新账单成功。
func TestProvisionRebuildsInvoiceWhenUnusable(t *testing.T) {
	var settleCalls, payCalls int
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			settleCalls++
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001","hostids":[555]}}`))
		case "/get_invoices_detail":
			if strings.Contains(r.URL.RawQuery, "9000") { // 旧账单已作废
				w.Write([]byte(`{"status":200,"data":{"detail":{"status":"Cancelled","total":"50.00"}}}`))
				return
			}
			w.Write([]byte(invoiceResp(`"50.00"`)))
		case "/apply_credit":
			payCalls++
			_ = r.ParseForm()
			if r.PostFormValue("invoiceid") == "9000" {
				w.Write([]byte(`{"status":400,"msg":"未找到支付项目"}`))
				return
			}
			w.Write([]byte(`{"status":200,"data":{"hostid":[555]}}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	_ = ck.SetCheckpoint(ckInvoice, "9000") // 上次留下、现已作废的账单
	res, err := p.Provision(context.Background(), cfg,
		server.ProvisionRequest{UpstreamPID: 1001, Cycle: "monthly", ExpectAmount: 50}, ck)
	if err != nil {
		t.Fatalf("失效账单应自动重建后付款成功，实得: %v", err)
	}
	if res.UpstreamHostID != 555 {
		t.Fatalf("应返回 host 555，实得 %d", res.UpstreamHostID)
	}
	if settleCalls != 1 {
		t.Fatalf("应重新结算一次，实得 %d 次", settleCalls)
	}
	if payCalls != 2 {
		t.Fatalf("应先付旧账单失败、再付新账单成功（共 2 次），实得 %d 次", payCalls)
	}
	if got, ok, _ := ck.GetCheckpoint(ckHosts); !ok || got != "555" {
		t.Fatalf("付款成功后应写 ckHosts=555，实得 %q", got)
	}
}

// 上游未返回账单号（显式 invoiceid=null）时必须报错停住：
// 拿 "null" 去付款只会得到"未找到支付项目"，还会把外键检查点写成 "null"。
func TestProvisionNullInvoiceRejected(t *testing.T) {
	var payCalled bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/clear":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/get_product_config":
			w.Write([]byte(`{"status":200,"data":{"currencyid":"1"}}`))
		case "/cart/add_to_shop":
			w.Write([]byte(`{"status":200,"msg":"ok"}`))
		case "/cart/settle":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":null}}`))
		case "/apply_credit":
			payCalled = true
			w.Write([]byte(`{"status":200}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	_, err := p.Provision(context.Background(), cfg,
		server.ProvisionRequest{UpstreamPID: 1001, Cycle: "monthly"}, ck)
	if err == nil || !strings.Contains(err.Error(), "结算未返回账单号") {
		t.Fatalf("上游未返回账单号应报错并说明原因，实得: %v", err)
	}
	if payCalled {
		t.Fatal("没有账单号时不该尝试支付")
	}
	if _, ok, _ := ck.GetCheckpoint(ckInvoice); ok {
		t.Fatal("不该把无效账单号写进检查点")
	}
}
