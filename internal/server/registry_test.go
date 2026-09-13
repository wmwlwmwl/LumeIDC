package server_test

import (
	"testing"

	"lumeidc/internal/server"
	"lumeidc/internal/server/easypanel"
	"lumeidc/internal/server/zjmf"
)

// 自检：ProductFormHintsSets 合并能力接口（MarkupFree 自动填充）与声明接口；
// zjmf 的产品表单差异已改由结构化 ProductFormSpec 声明（Hints 为零值）。
func TestProductFormHintsSets(t *testing.T) {
	r := server.NewRegistry()
	r.Register(zjmf.Provider{})
	r.Register(easypanel.Provider{})
	m := r.ProductFormHintsSets()
	ep, ok := m["easypanel"]
	if !ok || !ep.MarkupFree || ep.PIDHint == "" || len(ep.FieldSuggestions) == 0 {
		t.Fatalf("EP hints 合并异常: %+v", ep)
	}
	if zj := m["zjmf"]; zj.PIDHint != "" || zj.MarkupFree || len(zj.FieldSuggestions) != 0 {
		t.Fatalf("zjmf hints 应为零值: %+v", zj)
	}
}

// 自检：zjmf 通过 ProductFormSpec 声明上游商品下拉（替代旧 HTML 插槽）。
func TestZjmfProductFormSpec(t *testing.T) {
	fields := server.ProductFormSpecFor(zjmf.Provider{})
	if len(fields) != 1 {
		t.Fatalf("zjmf 应声明 1 个字段，实得 %d", len(fields))
	}
	f := fields[0]
	if f.Key != "upstream_pid" || f.OptionsURL == "" || !f.SyncName || !f.PullConfig {
		t.Fatalf("zjmf 上游商品字段声明异常: %+v", f)
	}
	ep := server.ProductFormSpecFor(easypanel.Provider{})
	if len(ep) != 1 || ep[0].ConfigByValue["cdn"] == nil {
		t.Fatalf("easypanel 站点类型字段声明异常: %+v", ep)
	}
}
