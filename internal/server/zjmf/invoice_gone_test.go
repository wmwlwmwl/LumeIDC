package zjmf

import (
	"context"
	"net/http"
	"testing"

	"lumeidc/internal/server"
)

// invoiceProbeEnv 起一个只回答账单查询的假上游。
func invoiceProbeEnv(t *testing.T, invoiceBody string) server.Config {
	t.Helper()
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/get_invoices_detail":
			w.Write([]byte(invoiceBody))
		default:
			http.NotFound(w, r)
		}
	})
}

// 账单仍待支付（Unpaid）：可以继续付款，不能判为失效——否则余额不足会误转人工。
func TestUpstreamInvoiceUnusableUnpaid(t *testing.T) {
	cfg := invoiceProbeEnv(t, `{"status":200,"data":{"detail":{"status":"Unpaid","total":"12.00"}}}`)
	unusable, err := upstreamInvoiceUnusable(context.Background(), cfg, "9001")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if unusable {
		t.Fatal("待支付账单应继续按可付处理")
	}
}

// 上游后台"删除账单"实际是置为 Cancelled：记录仍可查询，但永远付不掉，必须判定失效。
func TestUpstreamInvoiceUnusableCancelled(t *testing.T) {
	cfg := invoiceProbeEnv(t, `{"status":200,"data":{"detail":{"status":"Cancelled","paid_time":0}}}`)
	unusable, err := upstreamInvoiceUnusable(context.Background(), cfg, "9001")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !unusable {
		t.Fatal("已作废（Cancelled）的账单应判为失效，否则会拿它无限重试")
	}
}

// 状态字段缺失（上游版本差异/字段异常）：按仍可支付处理，不能把可付账单判死。
func TestUpstreamInvoiceUnusableMissingStatus(t *testing.T) {
	cfg := invoiceProbeEnv(t, `{"status":200,"data":{"detail":{"total":"12.00"}}}`)
	unusable, err := upstreamInvoiceUnusable(context.Background(), cfg, "9001")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if unusable {
		t.Fatal("状态字段缺失时应保守按可付处理")
	}
}

// 账单确实不存在：上游回业务错误。
func TestUpstreamInvoiceUnusableGone(t *testing.T) {
	cfg := invoiceProbeEnv(t, `{"status":400,"msg":"账单不存在"}`)
	unusable, err := upstreamInvoiceUnusable(context.Background(), cfg, "9001")
	if err != nil {
		t.Fatalf("上游业务错误不应作为探测失败: %v", err)
	}
	if !unusable {
		t.Fatal("账单不存在应判为失效")
	}
}

// 传输故障（HTTP 500）：不得据此判定账单失效，否则上游抖动会被误成需要转人工。
func TestUpstreamInvoiceUnusableTransportError(t *testing.T) {
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	unusable, err := upstreamInvoiceUnusable(context.Background(), cfg, "9001")
	if err == nil {
		t.Fatal("HTTP 500 应返回探测错误")
	}
	if unusable {
		t.Fatal("传输故障不得判定为账单失效")
	}
}
