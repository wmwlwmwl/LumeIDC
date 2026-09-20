package smsdk

import (
	"context"
	"net/http"
	"testing"
)

type fakeProvider struct{}

func (fakeProvider) Descriptor() ProviderDescriptor {
	d, _ := DescriptorFor("test_fake")
	return d
}
func (fakeProvider) SendSMS(context.Context, string, ProviderTemplate, map[string]string) (ProviderResult, error) {
	return ProviderResult{}, nil
}
func (fakeProvider) TemplateCreate(context.Context, ProviderTemplate) (ProviderResult, error) {
	return ProviderResult{}, nil
}
func (fakeProvider) TemplateUpdate(context.Context, ProviderTemplate) (ProviderResult, error) {
	return ProviderResult{}, nil
}
func (fakeProvider) TemplateQuery(context.Context, string, Range) (ProviderResult, error) {
	return ProviderResult{}, nil
}
func (fakeProvider) TemplateDelete(context.Context, string, Range) (ProviderResult, error) {
	return ProviderResult{}, nil
}

func fakeFactory(map[string]string, *http.Client) (Provider, error) { return fakeProvider{}, nil }

// 注册 → 查询 → 能力判定 → 构造 全链路。
func TestRegistryRoundTrip(t *testing.T) {
	RegisterSMSProvider(ProviderDescriptor{
		Key:          "test_fake",
		Name:         "测试商",
		ConfigFields: []string{"sms_access_key"},
		Capabilities: ProviderCapabilities{Ranges: []Range{RangeCN}, OTP: true},
	}, fakeFactory)

	d, ok := DescriptorFor("test_fake")
	if !ok || d.Name != "测试商" {
		t.Fatalf("DescriptorFor 未取回注册描述符: %+v ok=%v", d, ok)
	}
	if _, ok := DescriptorFor("no_such"); ok {
		t.Error("未注册 key 不应命中")
	}
	if Registry()["test_fake"].Key != "test_fake" {
		t.Error("Registry 枚举缺已注册服务商")
	}
	if !Supports("test_fake", RangeCN, "otp") {
		t.Error("已声明 CN+OTP 能力应支持")
	}
	if Supports("test_fake", RangeCN, "notification") {
		t.Error("未声明 notification 能力不应支持")
	}
	if Supports("test_fake", RangeGlobal, "otp") {
		t.Error("未声明 global 范围不应支持")
	}
	if Supports("test_fake", RangeMarketing, "otp") {
		t.Error("营销范围禁发 OTP")
	}
	if _, err := NewProvider("test_fake", nil, nil); err != nil {
		t.Fatalf("已注册服务商构造失败: %v", err)
	}
	if _, err := NewProvider("no_such", nil, nil); err == nil {
		t.Error("未注册服务商构造应报错")
	}
}

// 重复注册 key 必须 panic（启动期暴露冲突）。
func TestRegisterDuplicatePanics(t *testing.T) {
	d := ProviderDescriptor{Key: "test_dup"}
	RegisterSMSProvider(d, fakeFactory)
	defer func() {
		if recover() == nil {
			t.Fatal("重复注册应 panic")
		}
	}()
	RegisterSMSProvider(d, fakeFactory)
}

// 缺 key 必须 panic。
func TestRegisterEmptyKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺 Key 注册应 panic")
		}
	}()
	RegisterSMSProvider(ProviderDescriptor{}, fakeFactory)
}
