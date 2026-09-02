package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type adminServiceRow struct {
	ID       int64
	A, B, C  string // 用户 / 产品 / 状态
	D        string // 到期时间
	Upstream string // 上游 host id
	ProvErr  string // 开通失败原因
	Profit   string // 毛利（来自下单订单）
}

// ServicesList GET /admin/services — 全部服务列表。

func (m *AdminManage) ServicesList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	list, err := m.Svc.AdminList(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	out := make([]adminServiceRow, 0, len(list))
	for _, svc := range list {
		out = append(out, adminServiceRow{ID: svc.ID, A: svc.User, B: svc.Name, C: svc.Status, D: svc.Expires, Upstream: svc.Upstream, ProvErr: svc.ProvErr, Profit: svc.Profit})
	}
	m.renderAdmin(w, "admin_services.html", AdminData{
		Rows: out, CSRF: m.adminCSRF(w, r), Error: r.URL.Query().Get("err"),
	})
}

// ServicesStatusJSON GET /admin/services/status — 后台轮询用：返回各服务状态与失败原因，
// 用于自动发现“上游已开通/开通失败”并刷新列表（ponytail: 仅读 DB，不重复调上游）。

func (m *AdminManage) ServicesStatusJSON(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	rows, err := m.Svc.AdminStatus(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	type st struct {
		ID      int64  `json:"id"`
		C       string `json:"c"`
		ProvErr string `json:"provErr"`
	}
	out := make([]st, 0, len(rows))
	for _, s := range rows {
		out = append(out, st{ID: s.ID, C: s.StatusLabel, ProvErr: s.ProvErr})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ServiceAction POST /admin/services/{id}/action — 停机/解除/删除/重试开通。

func (m *AdminManage) ServiceAction(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PostFormValue("do")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var err error
	errMsg := ""
	switch action {
	case "suspend":
		err = m.Lifecycle.Suspend(ctx, id)
	case "unsuspend":
		err = m.Lifecycle.Unsuspend(ctx, id)
	case "terminate":
		err = m.Lifecycle.Terminate(ctx, id)
	case "retry":
		err = m.Payment.RetryProvision(ctx, id)
	default:
		http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
		return
	}
	if err != nil {
		errMsg = url.QueryEscape(err.Error())
	}
	m.audit(r, "service_"+action, "service", id, errMsg)
	http.Redirect(w, r, "/admin/services?err="+errMsg, http.StatusSeeOther)
}

// ---------- 用户管理操作 ----------

// UserEdit GET /admin/users/{id}/edit — 用户管理表单（联系方式、余额/状态/重置密码）。
