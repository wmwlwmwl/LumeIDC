package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestRegisterAndAll(t *testing.T) {
	Register(&testPlugin{name: "t_beta"})
	Register(&testPlugin{name: "t_alpha"})
	names := []string{}
	for _, p := range All() {
		names = append(names, p.Info().Name)
	}
	// All 按 Name 排序
	if len(names) < 2 || names[0] != "t_alpha" || names[1] != "t_beta" {
		t.Fatalf("All 应排序返回, 得到 %v", names)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register(&testPlugin{name: "t_dup"})
	defer func() {
		if recover() == nil {
			t.Fatal("重名注册应 panic")
		}
	}()
	Register(&testPlugin{name: "t_dup"})
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺名注册应 panic")
		}
	}()
	Register(&testPlugin{name: ""})
}

func TestEmitOrderAndErrorIsolation(t *testing.T) {
	var seq atomic.Int32
	var got1, got2 int32
	fail := errors.New("boom")
	Subscribe("t.event", func(ctx context.Context, payload any) error {
		got1 = payload.(int32)
		seq.Add(1)
		return fail // 返回错误不阻断后续订阅者
	})
	Subscribe("t.event", func(ctx context.Context, payload any) error {
		got2 = payload.(int32)
		seq.Add(1) // 第二个执行 → seq=2
		return nil
	})
	Emit(context.Background(), "t.event", int32(42))
	if got1 != 42 || got2 != 42 {
		t.Fatalf("payload 未正确传递: %d %d", got1, got2)
	}
	if seq.Load() != 2 {
		t.Fatalf("两个订阅者都应执行, seq=%d", seq.Load())
	}
}

func TestEmitPanicIsolation(t *testing.T) {
	done := false
	Subscribe("t.panic", func(ctx context.Context, payload any) error {
		panic("炸了")
	})
	Subscribe("t.panic", func(ctx context.Context, payload any) error {
		done = true
		return nil
	})
	Emit(context.Background(), "t.panic", nil) // 不应 panic 出来
	if !done {
		t.Fatal("前一个订阅者 panic 不应阻断后续订阅者")
	}
}

func TestEmitNoSubscriber(t *testing.T) {
	Emit(context.Background(), "t.nobody", nil) // 无订阅者时静默返回
}

type testPlugin struct{ name string }

func (p *testPlugin) Info() Info         { return Info{Name: p.name} }
func (p *testPlugin) Init(h *Host) error { return nil }
