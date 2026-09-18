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
	ID                                                                                                           int64
	Code, Driver, Name, APIURL, PID, Channel, PaymentMode, AppID, PrivateKey, PublicKey                          string
	Key                                                                                                          string // 易支付商户密钥（仅用于"是否已配置"判断，不回传）
	FeePercent                                                                                                   string
	Enabled                                                                                                      bool
	Sort                                                                                                         int
}

func (g *AdminGateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/gateway", g.form)
	mux.HandleFunc("POST /admin/gateway/save", g.save)
	mux.HandleFunc("POST /admin/gateway/{code}/delete", g.delete)
}

func (g *AdminGateway) require(w http.ResponseWriter, r *http.Request) bool {
	return adminRequire(w, r)
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
			APIURL: v.Config["api_url"], PID: v.Config["pid"], Channel: v.Config["channel"], PaymentMode: v.Config["payment_mode"], AppID: v.Config["app_id"], PrivateKey: v.Config["private_key"], PublicKey: v.Config["public_key"], Key: v.Config["key"], FeePercent: feePercent, Enabled: v.Enabled, Sort: v.Sort})
	}
	// 密钥类字段不回传（仅给是否已配置的标志）；编辑时留空即沿用旧值，
	// 与保存逻辑一致。
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, map[string]any{
			"id": v.ID, "code": v.Code, "driver": v.Driver, "name": v.Name,
			"api_url": v.APIURL, "pid": v.PID, "channel": v.Channel, "payment_mode": v.PaymentMode, "app_id": v.AppID,
			"fee_percent": v.FeePercent, "enabled": v.Enabled, "sort": v.Sort,
			"has_key":         v.Key != "",
			"has_private_key": v.PrivateKey != "",
			"has_public_key":  v.PublicKey != "",
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

func (g *AdminGateway) save(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
			middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
			return
		}
	}
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
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	code := strings.TrimSpace(fv("code"))
	driver := strings.TrimSpace(fv("driver"))
	name := strings.TrimSpace(fv("name"))
	if code == "" || driver == "" || name == "" || strings.ContainsAny(code, " /?&") {
		fail("网关编码、类型和名称不能为空，编码不能含空格或特殊字符")
		return
	}
	impl, ok := g.Gateways[driver]
	if !ok || impl.Driver() != driver {
		fail("支付插件类型不存在")
		return
	}
	sort, _ := strconv.Atoi(fv("sort"))
	feePercent, _, feeErr := moneyutil.ParsePercent(strings.TrimSpace(fv("fee_percent")))
	if feeErr != nil {
		fail("手续费率无效（请输入 0 到 100 之间、最多两位小数的百分比）")
		return
	}
	cfg := map[string]string{
		"api_url":      strings.TrimSpace(fv("api_url")),
		"pid":          strings.TrimSpace(fv("pid")),
		"channel":      strings.TrimSpace(fv("channel")),
		"payment_mode": strings.TrimSpace(fv("payment_mode")),
		"app_id":       strings.TrimSpace(fv("app_id")),
		"private_key":  strings.TrimSpace(fv("private_key")),
		"public_key":   strings.TrimSpace(fv("public_key")),
		"fee_percent":  feePercent,
	}
	old, oldErr := g.GwRepo.Get(r.Context(), code)
	key := strings.TrimSpace(fv("key"))
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
	enabled := fv("enabled") == "1"
	// 由网关自身声明配置校验规则，避免在通用后台处理器里写死支付品牌。
	// 仅启用时校验：允许先保存未填全凭据的停用草稿。
	if enabled {
		if validator, ok := impl.(gateway.ConfigValidator); ok {
			if err := validator.ValidateConfig(cfg); err != nil {
				fail(err.Error())
				return
			}
		}
	}
	if err := g.GwRepo.SaveConfig(r.Context(), code, driver, name, cfg, enabled, sort); err != nil {
		fail("保存失败")
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
		return
	}
	http.Redirect(w, r, "/admin/gateway?msg="+url.QueryEscape("已保存"), http.StatusSeeOther)
}

func (g *AdminGateway) delete(w http.ResponseWriter, r *http.Request) {
	if !g.require(w, r) {
		return
	}
	if jsonVals(r) == nil && !g.requireCSRF(w, r) {
		return
	}
	code := r.PathValue("code")
	if err := g.GwRepo.Delete(r.Context(), code); err != nil && !errors.Is(err, repo.ErrNotFound) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": "删除失败"})
			return
		}
		http.Redirect(w, r, "/admin/gateway?err="+url.QueryEscape("删除失败"), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
		return
	}
	http.Redirect(w, r, "/admin/gateway?msg="+url.QueryEscape("已删除"), http.StatusSeeOther)
}

func (g *AdminGateway) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
		return false
	}
	return true
}
