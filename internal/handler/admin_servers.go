package handler

import (
	"context"
	"log"
	"net/http"
	"net/url"
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
		log.Printf("[admin] 服务器列表查询失败: %v", err)
		http.Error(w, "查询失败", 500)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, sv := range list {
		status := "启用"
		if sv.Disabled {
			status = "停用"
		}
		out = append(out, map[string]any{
			"id": sv.ID, "name": sv.Name, "provider": sv.Provider, "api_url": sv.APIURL,
			"status": status, "disabled": sv.Disabled,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

func (s *AdminServers) Form(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	var serverRow *repo.Server
	// 凭据字段按 provider 返回：{"fields": {code: [字段...]}, "values": {列: 当前值}}
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
		serverRow = sv
		payload["values"] = map[string]string{
			"api_url": sv.APIURL, "api_username": sv.APIUsername,
			// 上游密钥不回显：provider 声明里该字段是 Secret，本意不下发浏览器。
			// 表单据此留空提交，由 Save 按"保持不变"处理（与后台其他账号类密钥一致）。
			"api_key": "",
		}
	}
	provs := make([]map[string]any, 0)
	for _, p := range s.Providers.List() {
		provs = append(provs, map[string]any{"code": p.Code, "name": p.Name})
	}
	out := map[string]any{
		"ok": 1, "providers": provs,
		"credential_fields": payload["fields"],
		"values":            payload["values"],
	}
	if serverRow != nil {
		out["server"] = map[string]any{
			"id": serverRow.ID, "name": serverRow.Name, "provider": serverRow.Provider,
			"api_url": serverRow.APIURL, "api_username": serverRow.APIUsername,
			"disabled": serverRow.Disabled, "profit_type": serverRow.ProfitType, "profit_value": serverRow.ProfitValue,
			"retry_later_enabled": serverRow.RetryLaterEnabled, "retry_later_minutes": serverRow.RetryLaterMinutes,
		}
	}
	writeJSON(w, out)
}

func (s *AdminServers) Save(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	fail := func(msg string) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/admin/servers?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	success := func() {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
			return
		}
		http.Redirect(w, r, "/admin/servers", http.StatusSeeOther)
	}
	name := strings.TrimSpace(fv("name"))
	apiURL := strings.TrimSpace(fv("api_url"))
	if name == "" || apiURL == "" {
		fail("名称与 API 地址必填")
		return
	}
	// provider 取表单值并按注册表白名单校验；编辑未提交 provider 时保留原值，防止误覆盖。
	// id 取自路由 /admin/servers/{id}/save；新增走 /admin/servers/save 时为空。
	idStr := r.PathValue("id")
	provider := strings.TrimSpace(fv("provider"))
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
		fail("不支持的上游类型: " + provider)
		return
	}
	sv := &repo.Server{
		Name: name, Provider: provider,
		APIURL:      apiURL,
		APIUsername: strings.TrimSpace(fv("api_username")),
		APIKey:      strings.TrimSpace(fv("api_key")),
		Disabled:    fv("disabled") == "1",
	}
	if pt, _ := strconv.ParseInt(fv("profit_type"), 10, 64); pt == 1 {
		sv.ProfitType = 1
	}
	sv.ProfitValue, _ = strconv.ParseFloat(fv("profit_value"), 64)
	if sv.ProfitValue < 0 {
		sv.ProfitValue = 0
	}
	// 只有显式传 "0" 才关闭自动重试；字段缺失按启用处理（与库中默认值一致）。
	sv.RetryLaterEnabled = fv("retry_later_enabled") != "0"
	if raw := strings.TrimSpace(fv("retry_later_minutes")); raw == "" {
		sv.RetryLaterMinutes = repo.DefaultRetryLaterMinutes
	} else {
		m, aerr := strconv.Atoi(raw)
		if aerr != nil || m < 1 || m > 1440 {
			fail("重试间隔需在 1~1440 分钟之间")
			return
		}
		sv.RetryLaterMinutes = m
	}
	if idStr == "" {
		if _, err := s.Servers.Create(r.Context(), sv); err != nil {
			fail(err.Error())
			return
		}
	} else {
		sv.ID, _ = strconv.ParseInt(idStr, 10, 64)
		// 上游密钥不回显（见 Form），表单留空表示"保持不变"：直接按空值写回，
		// 管理员改个名称就会把上游凭据清空、整台服务器失联。读不到原值宁可不保存。
		if sv.APIKey == "" {
			cur, gerr := s.Servers.Get(r.Context(), sv.ID)
			if gerr != nil {
				fail("读取原上游凭据失败，请重试")
				return
			}
			sv.APIKey = cur.APIKey
		}
		if err := s.Servers.Update(r.Context(), sv); err != nil {
			fail(err.Error())
			return
		}
	}
	success()
}

func (s *AdminServers) Delete(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Servers.Delete(r.Context(), id); err != nil {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/servers?err="+err.Error(), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
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
		jsonFail(w, "服务器不存在")
		return
	}
	prov, err := s.Providers.Get(sv.Provider)
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	cfg := server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := prov.TestConnection(ctx, cfg); err != nil {
		jsonFail(w, err.Error())
		return
	}
	msg := "连接成功"
	if bf, ok := prov.(server.BalanceFetcher); ok {
		if balance, berr := bf.FetchBalance(ctx, cfg); berr == nil && balance != "" {
			msg = "连接成功 · 余额: " + balance
		}
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": msg})
}
