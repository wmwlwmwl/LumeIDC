package handler

import (
	"net/http"
	"strconv"
)

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	c, _ := a.Stats.Counts(r.Context())
	trends, _ := a.Stats.Trends(r.Context(), 7)
	points := make([]map[string]any, 0, len(trends))
	for _, p := range trends {
		points = append(points, map[string]any{
			"date": p.Date, "users": p.Users, "orders": p.Orders, "revenue": p.Revenue,
		})
	}
	writeJSON(w, map[string]any{
		"ok": 1,
		"counts": map[string]any{
			"users": c.Users, "orders": c.Orders, "services": c.Services,
		},
		"trends": points,
	})
}

func (a *Admin) adminOrders(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	// 服务端分页查询（模板与 JSON 共用）
	const per = 25
	page := pageParam(r)
	q := r.URL.Query().Get("q")
	list, total, err := a.Stats.AdminOrdersPage(r.Context(), q, r.URL.Query().Get("sort"), r.URL.Query().Get("order"), per, (page-1)*per)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	items := make([]map[string]any, 0, len(list))
	var profit float64
	for _, o := range list {
		paid := ""
		if o.Paid != "0" && o.Paid != "0.00" {
			paid = o.Paid + "（手续费 " + o.Fee + "）"
		}
		if pf, perr := strconv.ParseFloat(o.Profit, 64); perr == nil {
			profit += pf
		}
		items = append(items, map[string]any{
			"id": o.ID, "email": o.Email, "amount": o.Amount, "cycle": o.Cycle,
			"status": o.Status, "profit": o.Profit, "paid": paid,
			"service_name": o.ServiceName, "service_host": o.ServiceHost, "service_status": o.ServiceStatus,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": items, "total": total, "page": page, "profit": strconv.FormatFloat(profit, 'f', 2, 64)})
}
