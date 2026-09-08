package handler

import (
	"net/http"
)

func (a *Admin) adminLogs(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	logs, err := a.AdminLog.List(r.Context(), 300)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	a.renderAdmin(w, "admin_logs.html", AdminData{Rows: logs})
}

// adminRefunds GET /admin/refunds — 退款记录列表。

func (a *Admin) adminRefunds(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := a.Refunds.List(r.Context(), 300)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	a.renderAdmin(w, "admin_refunds.html", AdminData{Rows: rows, Msg: r.URL.Query().Get("ok")})
}

// adminAnnouncements GET /admin/announcements — 公告列表。
