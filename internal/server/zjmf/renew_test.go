package zjmf

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"lumeidc/internal/server"
)

// fakeRenewUpstream 假上游：记录建单/付款调用，可控制付款结果与账单查询响应。
type fakeRenewUpstream struct {
	renewCalls  atomic.Int32
	payCalled   atomic.Bool
	payOK       bool
	invoiceBody string
	renewBody   string // /host/renew 响应体；空则返回带 invoiceid 的默认体
}

func (f *fakeRenewUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/renew":
			f.renewCalls.Add(1)
			if f.renewBody != "" {
				w.Write([]byte(f.renewBody))
				return
			}
			w.Write([]byte(`{"status":200,"msg":"ok","data":{"invoiceid":"7001"}}`))
		case "/get_invoices_detail":
			w.Write([]byte(f.invoiceBody))
		case "/apply_credit":
			f.payCalled.Store(true)
			if f.payOK {
				w.Write([]byte(`{"status":200,"data":{}}`))
				return
			}
			w.Write([]byte(`{"status":400,"msg":"账户余额不足"}`))
		default:
			http.NotFound(w, r)
		}
	}
}

// 续费账单检查点：重试复用同一张上游账单，避免"一直重试"把上游堆满未付账单。
func TestRenewInvoiceCheckpoint(t *testing.T) {
	var p Provider
	ctx := context.Background()

	t.Run("首次续费建单付款成功后清除检查点", func(t *testing.T) {
		f := &fakeRenewUpstream{payOK: true, invoiceBody: invoiceResp(`"10.00"`)}
		ck := newMemCheckpoint()
		if err := p.Renew(ctx, newUpgEnv(t, f.handler()), 555, "monthly", ck); err != nil {
			t.Fatalf("续费应成功: %v", err)
		}
		if f.renewCalls.Load() != 1 {
			t.Fatalf("首次应建 1 张账单，实得 %d", f.renewCalls.Load())
		}
		if _, ok, _ := ck.GetCheckpoint(ckRenewInvoice); ok {
			t.Fatal("成功后应清除账单检查点，否则下次续费会误复用这张已付账单")
		}
	})

	t.Run("检查点已有账单时不再新建", func(t *testing.T) {
		f := &fakeRenewUpstream{payOK: true, invoiceBody: invoiceResp(`"10.00"`)}
		ck := newMemCheckpoint()
		ck.m[ckRenewInvoice] = "7001"
		if err := p.Renew(ctx, newUpgEnv(t, f.handler()), 555, "monthly", ck); err != nil {
			t.Fatalf("复用已有账单应成功: %v", err)
		}
		if f.renewCalls.Load() != 0 {
			t.Fatalf("检查点已有账单时不应再调 /host/renew，实调 %d 次", f.renewCalls.Load())
		}
	})

	// 上游对 0 元商品直接续期、不生成账单（invoiceid=null）：必须按成功处理。
	// 当成失败重试会让每次重试都再调一次 /host/renew，上游因此多续一期。
	t.Run("上游未返回账单时按已直接续期处理", func(t *testing.T) {
		f := &fakeRenewUpstream{payOK: true, invoiceBody: invoiceResp(`"0.00"`),
			renewBody: `{"status":200,"msg":"ok","data":{"invoiceid":null}}`}
		ck := newMemCheckpoint()
		if err := p.Renew(ctx, newUpgEnv(t, f.handler()), 555, "monthly", ck); err != nil {
			t.Fatalf("上游直接续期应视为成功，实得: %v", err)
		}
		if f.payCalled.Load() {
			t.Fatal("没有账单就不该尝试支付")
		}
		if _, ok, _ := ck.GetCheckpoint(ckRenewInvoice); ok {
			t.Fatal("没有账单就不该写账单检查点")
		}
	})
}

// 续费失败分流：余额不足继续等、账单被删转人工。
func TestRenewFailureSplit(t *testing.T) {
	var p Provider
	ctx := context.Background()

	t.Run("余额不足保持重试并保留账单检查点", func(t *testing.T) {
		f := &fakeRenewUpstream{payOK: false, invoiceBody: invoiceResp(`"10.00"`)}
		ck := newMemCheckpoint()
		err := p.Renew(ctx, newUpgEnv(t, f.handler()), 555, "monthly", ck)
		if !server.IsRetryLater(err) {
			t.Fatalf("余额不足应保持重试，实得: %v", err)
		}
		if server.IsManualReview(err) {
			t.Fatal("余额不足不应转人工——充值后应能自动续上")
		}
		if !f.payCalled.Load() {
			t.Fatal("应尝试过付款")
		}
		if got, ok, _ := ck.GetCheckpoint(ckRenewInvoice); !ok || got != "7001" {
			t.Fatalf("应保留账单检查点供下次复用，实得 %q", got)
		}
	})

	t.Run("账单被删除转人工", func(t *testing.T) {
		f := &fakeRenewUpstream{payOK: false, invoiceBody: `{"status":400,"msg":"账单不存在"}`}
		ck := newMemCheckpoint()
		err := p.Renew(ctx, newUpgEnv(t, f.handler()), 555, "monthly", ck)
		if !server.IsManualReview(err) {
			t.Fatalf("账单已不存在应转人工，实得: %v", err)
		}
		if server.IsRetryLater(err) {
			t.Fatal("账单已消失不应保持重试")
		}
		// 失效账单的检查点必须清掉：否则后台重试会一直复用它、一直失败，永远出不去。
		if got, ok, _ := ck.GetCheckpoint(ckRenewInvoice); ok {
			t.Fatalf("失效账单的检查点应被清除，实得 %q", got)
		}
	})
}
