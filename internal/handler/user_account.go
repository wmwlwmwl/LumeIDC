package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

type userStats struct {
	ServiceCount int64
	ActiveCount  int64
	PaidTotal    string
	UnpaidCount  int64
}

func (h *Pages) stats(r *http.Request, userID int64) userStats {
	var s userStats
	s.ServiceCount, s.ActiveCount, s.UnpaidCount, s.PaidTotal = h.Svc.UserStats(r.Context(), userID)
	return s
}

func (h *Pages) userHome(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	bal, _ := h.Balance.Get(r.Context(), userID)
	st := h.stats(r, userID)
	writeJSON(w, map[string]any{
		"ok": 1,
		"stats": map[string]any{
			"service_count": st.ServiceCount, "active_count": st.ActiveCount,
			"paid_total": st.PaidTotal, "unpaid_count": st.UnpaidCount,
		},
		"balance":       bal,
		"announcements": announcementJSON(h.listAnnouncements(r.Context())),
	})
}

func (h *Pages) rechargeForm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	bal, _ := h.Balance.Get(r.Context(), userID)
	logs, _ := h.Balance.Logs(r.Context(), userID)
	if logs == nil {
		logs = []map[string]any{}
	}
	writeJSON(w, map[string]any{"ok": 1, "balance": bal, "logs": logs})
}

func (h *Pages) rechargeSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	vals := jsonVals(r)
	amount := ""
	if vals != nil {
		amount = strings.TrimSpace(vals["amount"])
	} else {
		tok := r.PostFormValue("_csrf")
		sess := middleware.FromSession(r.Context())
		if tok == "" || sess == nil || tok != sess.CSRFToken() {
			middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
			return
		}
		amount = strings.TrimSpace(r.PostFormValue("amount"))
	}
	id, err := h.Orders.CreateRechargeInvoice(r.Context(), userID, amount)
	if err != nil {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/user/recharge?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "invoice_id": id, "redirect": "/pay/" + strconv.FormatInt(id, 10)})
		return
	}
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func invoiceStatusText(status int16) string {
	switch status {
	case 0:
		return "未支付"
	case 1:
		return "已支付"
	case 3:
		return "已过期"
	default:
		return "作废"
	}
}

func invoiceRowsJSON(rows []repo.InvoiceRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, inv := range rows {
		out = append(out, map[string]any{
			"id": inv.ID, "no": inv.No, "amount": inv.Amount, "paid_amount": inv.PaidAmount,
			"fee_amount": inv.FeeAmount, "kind": inv.Kind, "status": invoiceStatusText(inv.Status),
			"due_at": inv.DueAt, "created_at": inv.CreatedAt,
			"service_id": inv.ServiceID, "service_name": inv.ServiceName, "service_host": inv.ServiceHost, "service_status": inv.ServiceStatus,
		})
	}
	return out
}

func (h *Pages) userInvoices(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	rows, err := h.Invoices.ListByUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": invoiceRowsJSON(rows)})
}

// serviceInvoices GET /services/{id}/invoices — 该服务关联的账单（账单跟随服务）。
func (h *Pages) serviceInvoices(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	rows, err := h.Invoices.ListByService(r.Context(), userID, id)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": invoiceRowsJSON(rows)})
}

func (h *Pages) passwordForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireUser(w, r); !ok {
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (h *Pages) passwordSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	oldPass := fv("old")
	newPass := fv("new")
	fail := func(msg string) {
		writeJSON(w, map[string]any{"ok": 0, "msg": msg})
	}
	if len(newPass) < 8 {
		fail("新密码至少 8 位")
		return
	}
	if err := h.UsersRepo.ChangePassword(r.Context(), userID, oldPass, newPass); err != nil {
		fail(err.Error())
		return
	}
	if h.PageStore != nil {
		h.PageStore.RevokeUser(userID)
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "密码已修改，请重新登录"})
}

// serviceRenew 用户续费自己的服务：生成续费账单并跳转支付页。
