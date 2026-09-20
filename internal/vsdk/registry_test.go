package vsdk

import (
	"context"
	"testing"
)

type fakeProvider struct{ key string }

func (p fakeProvider) Key() string { return p.key }
func (fakeProvider) Start(context.Context, StartRequest) (StartResult, error) {
	return StartResult{ProviderRef: "ref"}, nil
}
func (fakeProvider) Poll(context.Context, string) (Status, error) {
	return Status{Status: "approved"}, nil
}

func fakeFactory(h *Host) (Provider, error) { return fakeProvider{key: "test_fake"}, nil }

// 注册 → 查询 → 枚举 → 构造 全链路。
func TestRegistryRoundTrip(t *testing.T) {
	Register(Descriptor{
		Key:  "test_fake",
		Name: "测试实名",
		Fields: []ConfigField{
			{Key: "verification_fake_key", Label: "Key"},
			{Key: "verification_fake_secret", Label: "Secret", Secret: true},
		},
	}, fakeFactory)

	d, ok := DescriptorFor("test_fake")
	if !ok || d.Name != "测试实名" || len(d.Fields) != 2 {
		t.Fatalf("DescriptorFor 未取回注册描述符: %+v ok=%v", d, ok)
	}
	if !d.Fields[1].Secret {
		t.Error("Secret 标注丢失")
	}
	if _, ok := DescriptorFor("no_such"); ok {
		t.Error("未注册 key 不应命中")
	}
	if Registry()["test_fake"].Key != "test_fake" {
		t.Error("Registry 枚举缺已注册服务商")
	}
	p, found, err := New("test_fake", &Host{})
	if err != nil || !found || p.Key() != "test_fake" {
		t.Fatalf("已注册服务商构造失败: found=%v err=%v", found, err)
	}
	if _, found, err := New("no_such", &Host{}); found || err != nil {
		t.Error("未注册服务商构造应返回 found=false 且无错误")
	}
}

// 重复注册 key 必须 panic（启动期暴露冲突）。
func TestRegisterDuplicatePanics(t *testing.T) {
	d := Descriptor{Key: "test_dup"}
	Register(d, fakeFactory)
	defer func() {
		if recover() == nil {
			t.Fatal("重复注册应 panic")
		}
	}()
	Register(d, fakeFactory)
}

// 缺 key 必须 panic。
func TestRegisterEmptyKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺 Key 注册应 panic")
		}
	}()
	Register(Descriptor{}, fakeFactory)
}
