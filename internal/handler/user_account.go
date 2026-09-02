package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
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
	h.render(w, r, "user_home.html", map[string]any{
		"Stats":         h.stats(r, userID),
		"Balance":       bal,
		"Announcements": h.listAnnouncements(r.Context())})
}

func (h *Pages) rechargeForm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	bal, _ := h.Balance.Get(r.Context(), userID)
	h.render(w, r, "user_recharge.html", map[string]any{
		"Balance": bal, "CSRF": h.pageCSRF(w, r), "Error": r.URL.Query().Get("err"),
	})
}

func (h *Pages) rechargeSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return
	}
	id, err := h.Orders.CreateRechargeInvoice(r.Context(), userID, strings.TrimSpace(r.PostFormValue("amount")))
	if err != nil {
		http.Redirect(w, r, "/user/recharge?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

type invoiceRow struct {
	ID         int64
	No         string
	Amount     string
	PaidAmount string
	FeeAmount  string
	Kind       string
	Status     string
	CreatedAt  string
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
	list := make([]invoiceRow, 0, len(rows))
	for _, inv := range rows {
		item := invoiceRow{ID: inv.ID, No: inv.No, Amount: inv.Amount, PaidAmount: inv.PaidAmount, FeeAmount: inv.FeeAmount, Kind: inv.Kind, CreatedAt: inv.CreatedAt}
		switch inv.Status {
		case 0:
			item.Status = "未支付"
		case 1:
			item.Status = "已支付"
		default:
			item.Status = "作废"
		}
		list = append(list, item)
	}
	h.render(w, r, "user_invoices.html", map[string]any{"Invoices": list})
}

func (h *Pages) passwordForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireUser(w, r); !ok {
		return
	}
	h.render(w, r, "user_password.html", map[string]any{"CSRF": h.pageCSRF(w, r)})
}

func (h *Pages) passwordSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	oldPass := r.PostFormValue("old")
	newPass := r.PostFormValue("new")
	if len(newPass) < 8 {
		h.render(w, r, "user_password.html", map[string]any{"CSRF": h.pageCSRF(w, r), "Error": "新密码至少 8 位"})
		return
	}
	if err := h.UsersRepo.ChangePassword(r.Context(), userID, oldPass, newPass); err != nil {
		h.render(w, r, "user_password.html", map[string]any{"CSRF": h.pageCSRF(w, r), "Error": err.Error()})
		return
	}
	if h.PageStore != nil {
		h.PageStore.RevokeUser(userID)
	}
	h.render(w, r, "user_password.html", map[string]any{"CSRF": h.pageCSRF(w, r), "OK": true})
}

// serviceRenew 用户续费自己的服务：生成续费账单并跳转支付页。
