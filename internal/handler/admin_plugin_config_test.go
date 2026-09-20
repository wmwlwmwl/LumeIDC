package handler

import (
	"encoding/json"
	"testing"

	"lumeidc/internal/plugin"
)

func TestPluginConfigValidateField(t *testing.T) {
	h := &AdminPluginConfig{}
	raw := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}

	// switch
	if v, skip, msg := h.validateField(plugin.ConfigField{Key: "enabled", Type: "switch"}, raw(true)); msg != "" || skip || v != "1" {
		t.Fatalf("switch true → %q skip=%v msg=%q", v, skip, msg)
	}
	if v, _, msg := h.validateField(plugin.ConfigField{Key: "enabled", Type: "switch"}, raw(false)); msg != "" || v != "0" {
		t.Fatalf("switch false → %q msg=%q", v, msg)
	}
	if _, _, msg := h.validateField(plugin.ConfigField{Key: "enabled", Type: "switch"}, raw("yes")); msg == "" {
		t.Fatal("switch 非布尔应报错")
	}

	// number
	if v, _, msg := h.validateField(plugin.ConfigField{Key: "n", Type: "number"}, raw(12.5)); msg != "" || v != "12.5" {
		t.Fatalf("number → %q msg=%q", v, msg)
	}
	if _, _, msg := h.validateField(plugin.ConfigField{Key: "n", Type: "number"}, raw("abc")); msg == "" {
		t.Fatal("number 非数字应报错")
	}

	// select 白名单
	sel := plugin.ConfigField{Key: "s", Type: "select", Options: []plugin.ConfigOption{{Value: "a"}, {Value: "b"}}}
	if v, _, msg := h.validateField(sel, raw("a")); msg != "" || v != "a" {
		t.Fatalf("select 合法值 → %q msg=%q", v, msg)
	}
	if _, _, msg := h.validateField(sel, raw("c")); msg == "" {
		t.Fatal("select 白名单外应报错")
	}

	// multiselect 白名单过滤（非法项静默剔除）
	ms := plugin.ConfigField{Key: "m", Type: "multiselect", Options: []plugin.ConfigOption{{Value: "a"}, {Value: "b"}}}
	if v, _, msg := h.validateField(ms, raw([]string{"a", "x", "b"})); msg != "" || v != `["a","b"]` {
			t.Fatalf("multiselect 过滤 → %q msg=%q", v, msg)
	}

	// password：留空跳过（沿用旧值），非空正常写
	pw := plugin.ConfigField{Key: "secret", Type: "password"}
	if _, skip, msg := h.validateField(pw, raw("")); msg != "" || !skip {
		t.Fatal("password 留空应跳过写入")
	}
	if v, skip, _ := h.validateField(pw, raw(" abc ")); skip || v != "abc" {
		t.Fatalf("password 应 trim 保存: %q skip=%v", v, skip)
	}

	// text 默认
	if v, _, msg := h.validateField(plugin.ConfigField{Key: "u", Type: "text"}, raw(" https://x ")); msg != "" || v != "https://x" {
		t.Fatalf("text 应 trim: %q msg=%q", v, msg)
	}

	// multiselect 动态选项（OptionsRef=plugin.events 取事件目录）
	ev := plugin.ConfigField{Key: "events", Type: "multiselect", OptionsRef: "plugin.events"}
	v, _, msg := h.validateField(ev, raw([]string{plugin.EventOrderPaid, "not.an.event"}))
	if msg != "" || v != `["order.paid"]` {
		t.Fatalf("事件白名单过滤 → %q msg=%q", v, msg)
	}
}
