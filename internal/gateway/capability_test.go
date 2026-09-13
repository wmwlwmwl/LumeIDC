package gateway

import "testing"

// 补单依赖可选的 OrderQuerier 能力：只有实现了它的网关才参与订单查询补单。
// 当面付等暂时不支持主动查询的网关不应被误纳入补单任务。
func TestOrderQuerierCapability(t *testing.T) {
	if _, ok := any(Epay{}).(OrderQuerier); !ok {
		t.Fatal("易支付应实现 OrderQuerier，才能参与自动补单")
	}
	if _, ok := any(AlipayF2F{}).(OrderQuerier); !ok {
		t.Fatal("支付宝当面付应实现 OrderQuerier，内网补单依赖它")
	}
	if _, ok := any(Mock{}).(OrderQuerier); ok {
		t.Fatal("模拟支付未实现 OrderQuerier，不应被纳入补单")
	}
}

// 本地二维码结算页由可选能力驱动：当面付实现它，其它网关不实现，
// 通用处理器不得写死任何支付品牌。
func TestLocalCheckoutCapability(t *testing.T) {
	impl, ok := any(AlipayF2F{}).(LocalCheckout)
	if !ok {
		t.Fatal("支付宝当面付应实现 LocalCheckout，才能提供本地二维码结算页")
	}
	if impl.CheckoutPath() != LocalCheckoutPath {
		t.Fatalf("当面付结算页路径应为 %q，实际 %q", LocalCheckoutPath, impl.CheckoutPath())
	}
	if _, ok := any(Epay{}).(LocalCheckout); ok {
		t.Fatal("易支付走外部跳转，不应实现 LocalCheckout")
	}
	if _, ok := any(Mock{}).(LocalCheckout); ok {
		t.Fatal("模拟支付不应实现 LocalCheckout")
	}
}

// 配置校验由网关自身声明，通用后台不写死品牌。
func TestConfigValidatorCapability(t *testing.T) {
	validator, ok := any(AlipayF2F{}).(ConfigValidator)
	if !ok {
		t.Fatal("支付宝当面付应实现 ConfigValidator")
	}
	if err := validator.ValidateConfig(map[string]string{}); err == nil {
		t.Fatal("缺少应用私钥时应校验失败")
	}
	if err := validator.ValidateConfig(map[string]string{"private_key": "k"}); err != nil {
		t.Fatalf("已配置应用私钥时不应报错: %v", err)
	}
	epayValidator, ok := any(Epay{}).(ConfigValidator)
	if !ok {
		t.Fatal("易支付应实现 ConfigValidator，防止空密钥启用")
	}
	if err := epayValidator.ValidateConfig(map[string]string{"api_url": "https://x", "pid": "1"}); err == nil {
		t.Fatal("缺少商户密钥时应校验失败")
	}
	if err := epayValidator.ValidateConfig(map[string]string{"api_url": "https://x", "pid": "1", "key": "k"}); err != nil {
		t.Fatalf("凭据完整时不应报错: %v", err)
	}
}
