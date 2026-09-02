package handler

import (
	"net/http"
	"strconv"

	"lumeidc/internal/middleware"
)

func (a *Admin) require(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

// adminLogs GET /admin/logs — 操作审计日志。

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
