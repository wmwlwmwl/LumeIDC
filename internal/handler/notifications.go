package handler

import (
	"net/http"
	"strconv"

	"lumeidc/internal/middleware"
)

func (h *Pages) notificationID(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Notifier == nil {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("notificationID"), 10, 64)
	if err != nil || id <= 0 {
		jsonStatus(w, r, http.StatusBadRequest, "消息 ID 无效")
		return 0, 0, false
	}
	return userID, id, true
}

func (h *Pages) notificationUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Notifier == nil {
		return
	}
	_, _, unread, err := h.Notifier.ListPage(r.Context(), userID, "", "", 1, 1)
	if err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "unread": unread})
}

func (h *Pages) notificationMarkRead(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.notificationID(w, r)
	if !ok {
		return
	}
	if err := h.Notifier.MarkOneRead(r.Context(), userID, id); err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "操作失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (h *Pages) notificationMarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Notifier == nil {
		return
	}
	if err := h.Notifier.MarkRead(r.Context(), userID); err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "操作失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (h *Pages) notificationDelete(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := h.notificationID(w, r)
	if !ok {
		return
	}
	if err := h.Notifier.DeleteOne(r.Context(), userID, id); err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "操作失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (h *Pages) notificationDeleteAll(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Notifier == nil {
		return
	}
	if err := h.Notifier.DeleteAll(r.Context(), userID); err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "操作失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}
