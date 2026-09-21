package csdk

import (
	"context"
	"testing"
)

type fakeProvider struct{ key string }

func (f *fakeProvider) PublicConfig(ctx context.Context, scene string) PublicConfig {
	return PublicConfig{Scene: scene, Provider: f.key, Enabled: true}
}
func (f *fakeProvider) Verify(ctx context.Context, scene string, payload map[string]string, ip string) error {
	return nil
}

func TestRegisterAndNew(t *testing.T) {
	d := Descriptor{Key: "fake", Name: "测试验证", SDKURL: "https://example.com/sdk.js", SDKVersion: "v1"}
	Register(d, func(h *Host) (Provider, error) { return &fakeProvider{key: "fake"}, nil })

	got, ok := DescriptorFor("fake")
	if !ok || got.Name != "测试验证" || got.SDKURL != "https://example.com/sdk.js" {
		t.Fatalf("DescriptorFor 描述符不符: %+v ok=%v", got, ok)
	}
	if _, ok := DescriptorFor("nope"); ok {
		t.Fatal("未注册 key 不应命中")
	}
	p, found, err := New("fake", &Host{})
	if err != nil || !found || p == nil {
		t.Fatalf("New 已注册 key 失败: found=%v err=%v", found, err)
	}
	if _, found, err := New("nope", &Host{}); err != nil || found {
		t.Fatalf("未注册 key 应 found=false 且无错误: found=%v err=%v", found, err)
	}
	if _, ok := Registry()["fake"]; !ok {
		t.Fatal("Registry 应包含已注册 key")
	}
	cfg := d.BaseConfig("pub-id", "login")
	if cfg.Provider != "fake" || cfg.PublicID != "pub-id" || cfg.Scene != "login" ||
		cfg.SDKURL != "https://example.com/sdk.js" || cfg.ScriptURL != cfg.SDKURL || cfg.SDKVersion != "v1" {
		t.Fatalf("BaseConfig 构造不符: %+v", cfg)
	}
}

func TestRegisterDupPanics(t *testing.T) {
	Register(Descriptor{Key: "dup"}, func(h *Host) (Provider, error) { return &fakeProvider{}, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("重复注册应 panic")
		}
	}()
	Register(Descriptor{Key: "dup"}, func(h *Host) (Provider, error) { return &fakeProvider{}, nil })
}

func TestRegisterEmptyKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺 key 应 panic")
		}
	}()
	Register(Descriptor{Key: "  "}, func(h *Host) (Provider, error) { return &fakeProvider{}, nil })
}
