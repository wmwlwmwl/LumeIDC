package gateway

import "context"

// Mock 模拟支付网关（仅测试用）：无需任何外部配置，跳转到本站确认页，
// 点击确认即视为支付成功并核销账单。切勿在生产环境启用。
type Mock struct{}

func (Mock) Code() string { return "mock" }
func (Mock) Name() string { return "模拟支付（测试）" }

func (Mock) PayURL(ctx context.Context, req PayRequest) (string, error) {
	return "/mock/pay/" + req.InvoiceNo, nil
}
