package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

func adminRequire(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

const requestTimeout = 15 * time.Second

func serverConfig(sv *repo.Server) server.Config {
	return server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
}

func sqlNull(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id > 0}
}

func money(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
