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
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, map[string]any{
			"id": l.ID, "admin_id": l.AdminID, "admin_name": l.AdminName, "action": l.Action,
			"target_type": l.TargetType, "target_id": l.TargetID, "target_email": l.TargetEmail,
			"detail": l.Detail,
			"ip":     l.IP, "created_at": l.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// adminRefunds GET /admin/refunds — 退款记录列表。

func (a *Admin) adminRefunds(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := a.Refunds.List(r.Context(), 300)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, rf := range rows {
		out = append(out, map[string]any{
			"id": rf.ID, "user_id": rf.UserID, "order_id": rf.OrderID, "invoice_id": rf.InvoiceID,
			"amount": rf.Amount, "method": rf.Method, "reason": rf.Reason,
			"admin_id": rf.AdminID, "status": rf.Status,
			"created_at": rf.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// adminAnnouncements GET /admin/announcements — 公告列表。
