package handler

import (
	"net/http"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

type AdminGateway struct {
	GwRepo *repo.Gateways
}

func (g *AdminGateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/gateway", g.form)
	mux.HandleFunc("POST /admin/gateway", g.save)
}

func (g *AdminGateway) require(w http.ResponseWriter, r *http.Request) bool {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return false
	}
	return true
}

func (g *AdminGateway) form(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	cfg, _ := g.GwRepo.Config(r.Context(), "epay")
	renderAdmin(w, "admin_gateway.html", AdminData{
		CSRF: csrfOf(adminSessions, w, r),
		Rows: map[string]string{
			"APIURL": cfg["api_url"], "PID": cfg["pid"], "Key": cfg["key"], "Channel": cfg["channel"],
		},
	})
}

func (g *AdminGateway) save(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return
	}
	cfg := map[string]string{
		"api_url": strings.TrimSpace(r.PostFormValue("api_url")),
		"pid":     strings.TrimSpace(r.PostFormValue("pid")),
		"key":     strings.TrimSpace(r.PostFormValue("key")),
		"channel": strings.TrimSpace(envOr2(r.PostFormValue("channel"), "alipay")),
	}
	if err := g.GwRepo.SaveConfig(r.Context(), "epay", "易支付", cfg); err != nil {
		http.Error(w, "保存失败: "+err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/admin/gateway", http.StatusSeeOther)
}

func envOr2(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
