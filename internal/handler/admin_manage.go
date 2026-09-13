package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type AdminManage struct {
	Products    *repo.Products
	Users       *repo.Users
	Servers     *repo.Servers
	Balance     *repo.Balance
	Svc         *service.ServicesRepo
	Lifecycle   *service.Lifecycle
	Payment     *service.Payment
	Providers   *server.Registry
	Settings    *repo.Settings
	Identity    *repo.IdentityStore
	IdentitySvc *service.Identity // 实名解密（AdminSubmission）
	AdminLog    *repo.AdminLog
	CancelReqs  *repo.CancelRequests
	Notifier    *service.Notifier
	*Deps
}

func (m *AdminManage) require(w http.ResponseWriter, r *http.Request) bool {
	return adminRequire(w, r)
}

// audit 记录后台操作审计（失败仅记日志，不阻断主流程）。

func (m *AdminManage) audit(r *http.Request, action, targetType string, targetID int64, detail string) {
	var aid int64
	if s := middleware.FromSession(r.Context()); s != nil {
		aid = s.UserID
	}
	m.AdminLog.Record(aid, action, targetType, targetID, detail, r.RemoteAddr)
}

// ---------- 分类管理 ----------

func (m *AdminManage) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
		middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
		return false
	}
	return true
}

func checkCSRF(r *http.Request, tok string) bool {
	sess := middleware.FromSession(r.Context())
	return sess != nil && sess.CSRFToken() == tok
}

// ---------- 产品管理 ----------

func (m *AdminManage) OrderRefund(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	amount := strings.TrimSpace(fv("amount"))
	reason := strings.TrimSpace(fv("reason"))
	method := fv("method")
	if method == "" {
		method = "balance" // 默认退余额（与 SSR 表单默认项一致）
	}
	var aid int64
	if s := middleware.FromSession(r.Context()); s != nil {
		aid = s.UserID
	}
	if err := m.Payment.Refund(r.Context(), aid, id, amount, reason, method); err != nil {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/orders?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	m.audit(r, "refund", "order", id, amount+" via "+method)
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "退款已执行"})
		return
	}
	http.Redirect(w, r, "/admin/refunds?ok=1", http.StatusSeeOther)
}
