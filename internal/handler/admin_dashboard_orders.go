package handler

import (
	"net/http"
	"strconv"

	"lumeidc/internal/middleware"
)

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	c, _ := a.Stats.Counts(r.Context())
	rows := []adminRow{
		{ID: 1, A: "用户数", B: itoa(c.Users)},
		{ID: 2, A: "已支付订单", B: itoa(c.Orders)},
		{ID: 3, A: "激活服务", B: itoa(c.Services)},
	}
	a.renderAdmin(w, "admin_dashboard.html", AdminData{Rows: rows})
}

func (a *Admin) adminOrders(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	list, err := a.Stats.AdminOrders(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	var out []adminRow
	var total float64
	for _, o := range list {
		rw := adminRow{ID: o.ID, A: o.Email, B: o.Amount, C: o.Cycle, D: o.Status, E: o.Profit}
		if o.Paid != "0" && o.Paid != "0.00" {
			rw.F = o.Paid + "（手续费 " + o.Fee + "）"
		}
		if pf, perr := strconv.ParseFloat(o.Profit, 64); perr == nil {
			total += pf
		}
		out = append(out, rw)
	}
	a.renderAdmin(w, "admin_orders.html", AdminData{
		Rows:        out,
		CSRF:        a.adminCSRF(w, r),
		Error:       r.URL.Query().Get("err"),
		TotalProfit: strconv.FormatFloat(total, 'f', 2, 64),
	})
}
