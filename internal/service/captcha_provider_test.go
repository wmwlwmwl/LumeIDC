package service

import (
	"testing"

	"lumeidc/internal/csdk"
	// 注册验证码适配器：描述符断言依赖 init 自注册。
	_ "lumeidc/internal/plugins/captcha"
)

// 三家内置供应商的 SDK 地址必须在描述符白名单内（防误改/回收原 captchaSDK 硬编码的护栏）。
func TestExternalCaptchaDescriptorAllowlist(t *testing.T) {
	cases := map[string]string{
		"geetest":  "https://static.geetest.com/v4/gt4.js",
		"vaptcha":  "https://cdn4.vaptcha.com/src/v4.js",
		"corptcha": "https://res.25y.cn/corptcha/corptcha.iife.js",
	}
	for key, want := range cases {
		d, ok := csdk.DescriptorFor(key)
		if !ok {
			t.Fatalf("供应商 %s 未注册", key)
		}
		if d.SDKURL != want || d.SDKVersion == "" {
			t.Fatalf("供应商 %s 描述符不符: %+v", key, d)
		}
		if len(d.Fields) == 0 {
			t.Fatalf("供应商 %s 未声明配置字段", key)
		}
	}
	if _, ok := csdk.DescriptorFor("unknown"); ok {
		t.Fatal("未知供应商不应注册")
	}
}
