package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/gateway"
	"lumeidc/internal/middleware"
	moneyutil "lumeidc/internal/money"
	"lumeidc/internal/repo"
)

type AdminGateway struct {
	GwRepo   *repo.Gateways
	Gateways map[string]gateway.Gateway
	*Deps
}

type adminGatewayRow struct {
	ID                                                                     int64
	Code, Driver, Name, APIURL, PID, Channel, AppID, PrivateKey, PublicKey string
	FeePercent                                                             string
	Enabled                                                                bool
	Sort                                                                   int
}

func (g *AdminGateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/gateway", g.form)
	mux.HandleFunc("POST /admin/gateway/save", g.save)
	mux.HandleFunc("POST /admin/gateway/{code}/delete", g.delete)
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
	list, err := g.GwRepo.List(r.Context())
	if err != nil {
		http.Error(w, "查询支付网关失败", 500)
		return
	}
	rows := make([]adminGatewayRow, 0, len(list))
	for _, v := range list {
		feePercent, _, feeErr := moneyutil.ParsePercent(v.Config["fee_percent"])
		if feeErr != nil {
			feePercent = v.Config["fee_percent"]
		}
		rows = append(rows, adminGatewayRow{ID: v.ID, Code: v.Code, Driver: v.Driver, Name: v.Name,
			APIURL: v.Config["api_url"], PID: v.Config["pid"], Channel: v.Config["channel"], AppID: v.Config["app_id"], PrivateKey: v.Config["private_key"], PublicKey: v.Config["public_key"], FeePercent: feePercent, Enabled: v.Enabled, Sort: v.Sort})
	}
	g.renderAdmin(w, "admin_gateway.html", AdminData{Rows: rows, CSRF: g.adminCSRF(w, r), Error: r.URL.Query().Get("err"), Msg: r.URL.Query().Get("msg")})
}

func (g *AdminGateway) save(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	driver := strings.TrimSpace(r.PostFormValue("driver"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if code == "" || driver == "" || name == "" || strings.ContainsAny(code, " /?&") {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("网关编码、类型和名称不能为空，编码不能含空格或特殊字符"), http.StatusSeeOther)
		return
	}
	impl, ok := g.Gateways[driver]
	if !ok || impl.Driver() != driver {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("支付插件类型不存在"), http.StatusSeeOther)
		return
	}
	sort, _ := strconv.Atoi(r.PostFormValue("sort"))
	feePercent, _, feeErr := moneyutil.ParsePercent(strings.TrimSpace(r.PostFormValue("fee_percent")))
	if feeErr != nil {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("手续费率无效（请输入 0 到 100 之间、最多两位小数的百分比）"), http.StatusSeeOther)
		return
	}
	cfg := map[string]string{
		"api_url":     strings.TrimSpace(r.PostFormValue("api_url")),
		"pid":         strings.TrimSpace(r.PostFormValue("pid")),
		"channel":     strings.TrimSpace(r.PostFormValue("channel")),
		"app_id":      strings.TrimSpace(r.PostFormValue("app_id")),
		"private_key": strings.TrimSpace(r.PostFormValue("private_key")),
		"public_key":  strings.TrimSpace(r.PostFormValue("public_key")),
		"fee_percent": feePercent,
	}
	old, oldErr := g.GwRepo.Get(r.Context(), code)
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" && oldErr == nil {
		key = old.Config["key"]
	}
	cfg["key"] = key
	if oldErr == nil {
		for _, name := range []string{"private_key", "public_key"} {
			if cfg[name] == "" {
				cfg[name] = old.Config[name]
			}
		}
	}
	if driver == "alipay_f2f" && cfg["private_key"] == "" {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("支付宝当面付必须填写应用私钥"), http.StatusSeeOther)
		return
	}
	enabled := r.PostFormValue("enabled") == "1"
	if err := g.GwRepo.SaveConfig(r.Context(), code, driver, name, cfg, enabled, sort); err != nil {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("保存失败"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/gateway?msg="+url.QueryEscape("已保存"), http.StatusSeeOther)
}

func (g *AdminGateway) delete(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	if !g.requireCSRF(w, r) {
		return
	}
	code := r.PathValue("code")
	if err := g.GwRepo.Delete(r.Context(), code); err != nil && !errors.Is(err, repo.ErrNotFound) {
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("删除失败"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/gateway?msg="+url.QueryEscape("已删除"), http.StatusSeeOther)
}

func (g *AdminGateway) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return false
	}
	return true
}
