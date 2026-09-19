package plugin

import (
	"context"
	"errors"
	"testing"
)

// 禁用插件的订阅者不参与事件分发；启用后恢复。
func TestEmitSkipsDisabledPlugin(t *testing.T) {
	h := &Host{pluginName: "t_toggle"}
	calls := 0
	h.Subscribe("t.toggle", func(ctx context.Context, payload any) error {
		calls++
		return nil
	})
	Emit(context.Background(), "t.toggle", nil)
	if calls != 1 {
		t.Fatalf("默认启用应被调用, calls=%d", calls)
	}
	enabledMu.Lock()
	enabledMemo["t_toggle"] = false
	enabledMu.Unlock()
	Emit(context.Background(), "t.toggle", nil)
	if calls != 1 {
		t.Fatalf("禁用后不应被调用, calls=%d", calls)
	}
	enabledMu.Lock()
	delete(enabledMemo, "t_toggle")
	enabledMu.Unlock()
	Emit(context.Background(), "t.toggle", nil)
	if calls != 2 {
		t.Fatalf("恢复启用后应被调用, calls=%d", calls)
	}
}

// 通配 "*" 订阅接收全部事件，且能通过 EventName 区分。
func TestWildcardSubscriber(t *testing.T) {
	h := &Host{pluginName: "t_wild"}
	var got []string
	h.Subscribe("*", func(ctx context.Context, payload any) error {
		got = append(got, EventName(ctx))
		return nil
	})
	Emit(context.Background(), "t.w1", nil)
	Emit(context.Background(), "t.w2", nil)
	if len(got) != 2 || got[0] != "t.w1" || got[1] != "t.w2" {
		t.Fatalf("通配订阅应按序收到事件名, got=%v", got)
	}
}

// ApplyFilters：链式改值；出错/panic/禁用均保留当前值继续。
func TestApplyFilters(t *testing.T) {
	h1 := &Host{pluginName: "t_f1"}
	h2 := &Host{pluginName: "t_f2"}
	h3 := &Host{pluginName: "t_f3"}
	h1.SubscribeFilter("t.filter", func(ctx context.Context, v any) (any, error) {
		return v.(string) + "A", nil
	})
	h2.SubscribeFilter("t.filter", func(ctx context.Context, v any) (any, error) {
		return "", errors.New("boom") // 出错：保留当前值
	})
	h3.SubscribeFilter("t.filter", func(ctx context.Context, v any) (any, error) {
		panic("炸了") // panic：保留当前值
	})
	h3.SubscribeFilter("t.filter", func(ctx context.Context, v any) (any, error) {
		return v.(string) + "B", nil
	})
	out := ApplyFilters(context.Background(), "t.filter", "x")
	if out.(string) != "xAB" {
		t.Fatalf("链式过滤结果错误: %v", out)
	}
	// 禁用 h3 后 +B 不再生效
	enabledMu.Lock()
	enabledMemo["t_f3"] = false
	enabledMu.Unlock()
	defer func() {
		enabledMu.Lock()
		delete(enabledMemo, "t_f3")
		enabledMu.Unlock()
	}()
	out = ApplyFilters(context.Background(), "t.filter", "x")
	if out.(string) != "xA" {
		t.Fatalf("禁用插件的过滤器应跳过: %v", out)
	}
}

// 事件目录：核心事件已预注册；插件可登记自定义事件。
func TestEventCatalog(t *testing.T) {
	RegisterEvent("t.custom", "测试事件")
	found := map[string]string{}
	for _, e := range EventCatalog() {
		found[e.Name] = e.Label
	}
	for _, name := range []string{EventOrderPaid, EventUserLogin, EventServiceRenewed, EventInvoiceExpired, "t.custom"} {
		if found[name] == "" {
			t.Fatalf("事件目录缺少 %s", name)
		}
	}
}

// Host.Config：键前缀 plugin.{name}.；未设置返回空串。
func TestHostConfigKeyPrefix(t *testing.T) {
	store := &fakeSettings{data: map[string]string{"plugin.t_cfg.url": "https://x"}}
	h := &Host{Settings: store, pluginName: "t_cfg"}
	if got := h.Config(context.Background(), "url"); got != "https://x" {
		t.Fatalf("Config 读取错误: %q", got)
	}
	if got := h.Config(context.Background(), "missing"); got != "" {
		t.Fatalf("缺失键应返回空串: %q", got)
	}
}

type fakeSettings struct{ data map[string]string }

func (f *fakeSettings) Get(ctx context.Context, key string) (string, error) {
	if v, ok := f.data[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeSettings) Set(ctx context.Context, key, value string) error {
	f.data[key] = value
	return nil
}
