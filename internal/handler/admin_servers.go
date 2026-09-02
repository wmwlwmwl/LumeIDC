package handler

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

type AdminServers struct {
	Servers   *repo.Servers
	Providers *server.Registry
	*Deps
}

func (s *AdminServers) require(w http.ResponseWriter, r *http.Request) bool {
	return adminRequire(w, r)
}

func (s *AdminServers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/servers", s.List)
	mux.HandleFunc("GET /admin/servers/new", s.Form)
	mux.HandleFunc("POST /admin/servers/save", s.Save)
	mux.HandleFunc("GET /admin/servers/{id}/edit", s.Form)
	mux.HandleFunc("POST /admin/servers/{id}/save", s.Save)
	mux.HandleFunc("POST /admin/servers/{id}/delete", s.Delete)
	mux.HandleFunc("GET /admin/servers/{id}/test", s.TestConn)
}

func (s *AdminServers) List(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	list, err := s.Servers.List(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	var rows []adminRow
	for _, sv := range list {
		d := "启用"
		if sv.Disabled {
			d = "停用"
		}
		rows = append(rows, adminRow{
			ID: sv.ID, A: sv.Name, B: sv.Provider,
			C: sv.APIURL, D: d,
		})
	}
	s.renderAdmin(w, "admin_servers.html", AdminData{
		Rows: rows, CSRF: s.adminCSRF(w, r), Error: r.URL.Query().Get("err"),
	})
}

func (s *AdminServers) Form(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	data := AdminData{CSRF: s.adminCSRF(w, r), Providers: s.Providers.List()}
	// 凭据字段按 provider 动态渲染：{"fields": {code: [字段...]}, "values": {列: 当前值}}
	payload := map[string]any{
		"fields": s.Providers.CredentialFieldSets(),
		"values": map[string]string{},
	}
	if idStr := r.PathValue("id"); idStr != "" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		sv, err := s.Servers.Get(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Product = sv
		payload["values"] = map[string]string{
			"api_url": sv.APIURL, "api_username": sv.APIUsername, "api_key": sv.APIKey,
		}
	}
	if b, err := json.Marshal(payload); err == nil {
		data.ProviderFieldsJSON = template.JS(b)
	}
	s.renderAdmin(w, "admin_server_form.html", data)
}

func (s *AdminServers) Save(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	apiURL := strings.TrimSpace(r.PostFormValue("api_url"))
	if name == "" || apiURL == "" {
		http.Redirect(w, r, "/admin/servers?err=名称与 API 地址必填", http.StatusSeeOther)
		return
	}
	// provider 取表单值并按注册表白名单校验；编辑未提交 provider 时保留原值，防止误覆盖。
	// id 取自路由 /admin/servers/{id}/save；新增走 /admin/servers/save 时为空。
	idStr := r.PathValue("id")
	provider := strings.TrimSpace(r.PostFormValue("provider"))
	if provider == "" {
		if idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				if old, gerr := s.Servers.Get(r.Context(), id); gerr == nil {
					provider = old.Provider
				}
			}
		}
	}
	if provider == "" {
		provider = "zjmf"
	}
	valid := false
	for _, p := range s.Providers.List() {
		if p.Code == provider {
			valid = true
			break
		}
	}
	if !valid {
		http.Redirect(w, r, "/admin/servers?err=不支持的上游类型: "+provider, http.StatusSeeOther)
		return
	}
	sv := &repo.Server{
		Name: name, Provider: provider,
		APIURL:      apiURL,
		APIUsername: strings.TrimSpace(r.PostFormValue("api_username")),
		APIKey:      strings.TrimSpace(r.PostFormValue("api_key")),
		Disabled:    r.PostFormValue("disabled") == "1",
	}
	if pt, _ := strconv.ParseInt(r.PostFormValue("profit_type"), 10, 64); pt == 1 {
		sv.ProfitType = 1
	}
	sv.ProfitValue, _ = strconv.ParseFloat(r.PostFormValue("profit_value"), 64)
	if sv.ProfitValue < 0 {
		sv.ProfitValue = 0
	}
	if idStr == "" {
		if _, err := s.Servers.Create(r.Context(), sv); err != nil {
			http.Redirect(w, r, "/admin/servers?err="+err.Error(), http.StatusSeeOther)
			return
		}
	} else {
		sv.ID, _ = strconv.ParseInt(idStr, 10, 64)
		if err := s.Servers.Update(r.Context(), sv); err != nil {
			http.Redirect(w, r, "/admin/servers?err="+err.Error(), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/admin/servers", http.StatusSeeOther)
}

func (s *AdminServers) Delete(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Servers.Delete(r.Context(), id); err != nil {
		http.Redirect(w, r, "/admin/servers?err="+err.Error(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/servers", http.StatusSeeOther)
}

// TestConn GET /admin/servers/{id}/test — 测试上游连通性并返回 JSON 结果。
func (s *AdminServers) TestConn(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sv, err := s.Servers.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": "服务器不存在"})
		return
	}
	prov, err := s.Providers.Get(sv.Provider)
	if err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": err.Error()})
		return
	}
	cfg := server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := prov.TestConnection(ctx, cfg); err != nil {
		writeJSON(w, map[string]string{"ok": "0", "msg": err.Error()})
		return
	}
	msg := "连接成功"
	if bf, ok := prov.(server.BalanceFetcher); ok {
		if balance, berr := bf.FetchBalance(ctx, cfg); berr == nil && balance != "" {
			msg = "连接成功 · 余额: " + balance
		}
	}
	writeJSON(w, map[string]string{"ok": "1", "msg": msg})
}
