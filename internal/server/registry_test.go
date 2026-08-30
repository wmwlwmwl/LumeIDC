package server_test

import (
	"testing"

	"lumeidc/internal/server"
	"lumeidc/internal/server/easypanel"
	"lumeidc/internal/server/zjmf"
)

// 自检：ProductFormHintsSets 合并能力接口（MarkupFree 自动填充）与声明接口；
// 未声明的供应商（zjmf）应为零值。
func TestProductFormHintsSets(t *testing.T) {
	r := server.NewRegistry()
	r.Register(zjmf.Provider{})
	r.Register(easypanel.Provider{})
	m := r.ProductFormHintsSets()
	ep, ok := m["easypanel"]
	if !ok || !ep.MarkupFree || ep.PIDHint == "" || len(ep.FieldSuggestions) == 0 {
		t.Fatalf("EP hints 合并异常: %+v", ep)
	}
	if zj := m["zjmf"]; zj.PIDHint == "" || zj.MarkupFree {
		t.Fatalf("zjmf 应仅有 PIDHint: %+v", zj)
	}
}
