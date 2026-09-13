package handler

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
)

// CancelRequestsList GET /admin/service-cancel-requests — 停用申请列表（可按状态筛选）。
func (m *AdminManage) CancelRequestsList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if m.CancelReqs == nil {
		writeJSON(w, map[string]any{"ok": 1, "list": []any{}, "pending": 0})
		return
	}
	rows, err := m.CancelReqs.AdminList(r.Context(), status)
	if err != nil {
		log.Printf("[admin] 停用申请列表查询失败: %v", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	pending, _ := m.CancelReqs.PendingCount(r.Context())
	list := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		item := map[string]any{
			"id": c.ID, "service_id": c.ServiceID, "user_id": c.UserID,
			"username": c.Username, "email": c.UserEmail,
			"service_name": c.ServiceName, "product_name": c.ProductName,
			"hostname": c.Hostname, "service_status": c.StatusText,
			"type": c.Type, "reason": c.Reason, "reason_detail": c.ReasonDetail,
			"status": c.Status, "handle_mode": c.HandleMode, "handle_note": c.HandleNote,
			"created_at": c.CreatedAt.Format("2006-01-02 15:04"),
			"handled_at": "",
		}
		if c.HandledAt.Valid {
			item["handled_at"] = c.HandledAt.Time.Format("2006-01-02 15:04")
		}
		if c.ExpiresAt.Valid {
			item["expires_at"] = c.ExpiresAt.Time.Format("2006-01-02")
		}
		list = append(list, item)
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list, "pending": pending})
}

// CancelRequestHandle POST /admin/service-cancel-requests/{id}/handle — 处理停用申请。
// action=local（本地删除，不动上游）/upstream（连上游删除）/reject（驳回）。
func (m *AdminManage) CancelRequestHandle(w http.ResponseWriter, r *http.Request) {
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
	action := strings.TrimSpace(fv("action"))
	note := strings.TrimSpace(fv("note"))
	if m.CancelReqs == nil {
		jsonStatus(w, r, http.StatusServiceUnavailable, "停用申请服务未启用")
		return
	}
	req, err := m.CancelReqs.Get(r.Context(), id)
	if err != nil || req == nil {
		jsonStatus(w, r, http.StatusNotFound, "申请不存在")
		return
	}
	if req.Status != "pending" {
		jsonStatus(w, r, http.StatusBadRequest, "该申请已处理")
		return
	}
	var adminID int64
	if s := middleware.FromSession(r.Context()); s != nil {
		adminID = s.UserID
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	fail := func(msg string) {
		writeJSON(w, map[string]any{"ok": 0, "msg": msg})
	}

	switch action {
	case "reject":
		claimed, err := m.CancelReqs.MarkHandled(ctx, id, "rejected", "", note, adminID)
		if err != nil {
			log.Printf("[admin] 停用申请驳回失败: %v", err)
			fail("操作失败")
			return
		}
		if !claimed {
			fail("该申请已处理")
			return
		}
		m.notifyCancelResult(ctx, req.UserID, false, note)
		m.Svc.AppendLog(ctx, req.ServiceID, req.UserID, "停用申请驳回", note)
		m.audit(r, "cancel_request_reject", "service_cancel_request", id, note)
		writeJSON(w, map[string]any{"ok": 1, "msg": "已驳回"})
	case "local", "upstream":
		// ponytail: 先原子占位（pending→approved）再执行删除，防并发/重复执行对同一服务
		// 二次删除；删除失败回滚为 pending 供重试。占位后进程崩溃的窗口无法用单库事务
		// 消除（删除含上游调用），靠 audit 日志与 handled_at 人工核对。
		claimed, err := m.CancelReqs.MarkHandled(ctx, id, "approved", action, note, adminID)
		if err != nil {
			log.Printf("[admin] 停用申请占位失败: %v", err)
			fail("操作失败")
			return
		}
		if !claimed {
			fail("该申请已处理")
			return
		}
		if action == "local" {
			err = m.Lifecycle.TerminateLocal(ctx, req.ServiceID)
		} else {
			err = m.Lifecycle.Terminate(ctx, req.ServiceID)
		}
		if err != nil {
			log.Printf("[admin] 停用申请删除失败（%s）: %v", action, err)
			if rbErr := m.CancelReqs.Reopen(ctx, id); rbErr != nil {
				log.Printf("[admin] 停用申请状态回滚失败（申请 %d 需人工核对）: %v", id, rbErr)
			}
			fail("删除失败：" + err.Error())
			return
		}
		m.notifyCancelResult(ctx, req.UserID, true, note)
		label := "本地删除"
		if action == "upstream" {
			label = "连上游删除"
		}
		m.Svc.AppendLog(ctx, req.ServiceID, req.UserID, "停用申请已通过", label)
		m.audit(r, "cancel_request_"+action, "service_cancel_request", id, note)
		writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
	default:
		jsonStatus(w, r, http.StatusBadRequest, "未知操作")
	}
}

// notifyCancelResult 审核结果站内信通知用户。
func (m *AdminManage) notifyCancelResult(ctx context.Context, userID int64, approved bool, note string) {
	if m.Notifier == nil {
		return
	}
	if approved {
		body := "你的服务停用申请已通过，服务已删除。"
		if note != "" {
			body += "\n备注：" + note
		}
		m.Notifier.Notify(ctx, userID, "停用申请已通过", body)
		return
	}
	body := "你的服务停用申请未通过。"
	if note != "" {
		body += "\n原因：" + note
	}
	m.Notifier.Notify(ctx, userID, "停用申请未通过", body)
}
