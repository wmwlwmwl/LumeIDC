package handler

import (
	"context"
	"net/http"
	"strconv"

	"lumeidc/internal/repo"
)

func loadGlobalProfitType(ctx context.Context, s *repo.Settings) int64 {
	if s == nil {
		return 0
	}
	v, _ := s.Get(ctx, "default_profit_type")
	n, _ := strconv.ParseInt(v, 10, 64)
	if n != 1 {
		n = 0
	}
	return n
}

func loadGlobalProfitValue(ctx context.Context, s *repo.Settings) float64 {
	if s == nil {
		return 0
	}
	v, _ := s.Get(ctx, "default_profit_value")
	f, _ := strconv.ParseFloat(v, 64)
	if f < 0 {
		f = 0
	}
	return f
}

// SaveGlobalProfit POST /admin/settings/profit — 保存全局默认利润。

func (m *AdminManage) SaveGlobalProfit(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) || !m.requireCSRF(w, r) {
		return
	}
	profitType, _ := strconv.ParseInt(r.PostFormValue("profit_type"), 10, 64)
	if profitType != 1 {
		profitType = 0
	}
	profitValue, _ := strconv.ParseFloat(r.PostFormValue("profit_value"), 64)
	if profitValue < 0 {
		profitValue = 0
	}
	m.Settings.Set(r.Context(), "default_profit_type", strconv.FormatInt(profitType, 10))
	m.Settings.Set(r.Context(), "default_profit_value", strconv.FormatFloat(profitValue, 'f', 2, 64))
	http.Redirect(w, r, "/admin/products?msg=全局利润已保存", http.StatusSeeOther)
}
