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
	Products  *repo.Products
	Users     *repo.Users
	Servers   *repo.Servers
	Balance   *repo.Balance
	Svc       *service.ServicesRepo
	Lifecycle *service.Lifecycle
	Payment   *service.Payment
	Providers *server.Registry
	Settings  *repo.Settings
	Identity  *repo.IdentityStore
	IdentitySvc *service.Identity // 实名解密（AdminSubmission）
	AdminLog  *repo.AdminLog
	*Deps
}

func (m *AdminManage) require(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
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

// typeRow 分类树行（两级，模板用）。

func (m *AdminManage) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
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
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	amount := strings.TrimSpace(r.PostFormValue("amount"))
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	method := r.PostFormValue("method")
	var aid int64
	if s := middleware.FromSession(r.Context()); s != nil {
		aid = s.UserID
	}
	if err := m.Payment.Refund(r.Context(), aid, id, amount, reason, method); err != nil {
		http.Redirect(w, r, "/admin/orders?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	m.audit(r, "refund", "order", id, amount+" via "+method)
	http.Redirect(w, r, "/admin/refunds?ok=1", http.StatusSeeOther)
}

// syncProductUpstream 按需同步单个产品的上游价格与库存（编辑表单打开时调用）。
