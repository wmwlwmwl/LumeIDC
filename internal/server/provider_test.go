package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// IsManualReview 决定任务是否进 manual_review、停止自动重试。
// 漏判会让 cron 反复重试一个永远不可能成功的开通；误判会让可自愈的失败被卡住。
func TestIsManualReview(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"人工复核错误", &ManualReviewError{Msg: "结算成功但检查点未落库"}, true},
		{"上游价格变动", &PriceChangedError{UpstreamAmount: 12, ExpectAmount: 10}, true},
		{"普通错误", errors.New("网络超时"), false},
		// 调用方包一层（%w）后仍须识别：漏判会退回自动重试，而这类失败重试永远不会成功。
		{"包装后的人工复核错误", fmt.Errorf("开通失败: %w", &ManualReviewError{Msg: "x"}), true},
		{"包装后的价格变动", fmt.Errorf("开通失败: %w", &PriceChangedError{UpstreamAmount: 12}), true},
		{"包装后的普通错误", fmt.Errorf("开通失败: %w", errors.New("网络超时")), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsManualReview(tt.err); got != tt.want {
				t.Fatalf("IsManualReview() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

// IsRetryLater 决定任务是否"保持重试且不消耗次数"。
// 漏判（当成普通失败）会让上游充值到账后任务已被判 dead；误判则永不放弃。
func TestIsRetryLater(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"等上游充值", &RetryLaterError{Msg: "账户余额不足"}, true},
		{"包装后的等充值", fmt.Errorf("续费失败: %w", &RetryLaterError{Msg: "x"}), true},
		{"人工复核错误", &ManualReviewError{Msg: "x"}, false},
		{"上游价格变动", &PriceChangedError{UpstreamAmount: 12}, false},
		{"普通错误", errors.New("网络超时"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryLater(tt.err); got != tt.want {
				t.Fatalf("IsRetryLater() = %v，期望 %v", got, tt.want)
			}
		})
	}
	// 两类必须互斥：余额不足要自动重试，绝不能同时被当成人工处理而停下。
	retryLater := &RetryLaterError{Msg: "账户余额不足"}
	if IsManualReview(retryLater) {
		t.Fatal("等充值不应被判定为人工复核，否则不会自动重试")
	}
}

// PriceChangedError 的文案直接显示给后台管理员，必须中文且带全两个金额与差额。
func TestPriceChangedErrorText(t *testing.T) {
	e := &PriceChangedError{
		UpstreamAmount:    12,
		ExpectAmount:      10,
		UpstreamInvoiceID: "9001",
		UpstreamPID:       1001,
	}
	msg := e.Error()
	for _, want := range []string{"12.00", "10.00", "贵 ￥2.00", "继续", "退款"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("文案缺少 %q：%s", want, msg)
		}
	}
	// 不应把英文标识或字段名直接透给管理员。
	for _, bad := range []string{"error", "UpstreamAmount", "UpstreamPID"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("文案不应包含 %q：%s", bad, msg)
		}
	}
}
