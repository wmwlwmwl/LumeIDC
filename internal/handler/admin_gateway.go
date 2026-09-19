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

// adminGatewayConfigFields 后台表单可提交、列表可回传的配置键。
// 新增驱动所需字段时在此追加即可，无需改动结构体与逐字段赋值。
var adminGatewayConfigFields = []string{
	"api_url", "pid", "key", "channel", "payment_mode", "mobile_qrcode",
	"app_id", "mch_id", "private_key", "public_key",
	"api_v3_key", "cert_serial", "public_key_id",
	"h5_app_name", "h5_app_url",
	// seller_id 选填：支付宝回调归属校验用（与 app_id 同属一个账号），留空则跳过该比对。
	"seller_id",
}

// adminGatewaySecretFields 密钥类配置：不回传原值（仅回报 has_<key> 是否已配置），
// 编辑时留空表示沿用旧值。
var adminGatewaySecretFields = []string{"key", "private_key", "public_key", "api_v3_key"}

func isAdminGatewaySecretField(key string) bool {
	for _, k := range adminGatewaySecretFields {
		if k == key {
			return true
		}
	}
	return false
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
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		feePercent, _, feeErr := moneyutil.ParsePercent(v.Config["fee_percent"])
		if feeErr != nil {
			feePercent = v.Config["fee_percent"]
		}
		row := map[string]any{
			"id": v.ID, "code": v.Code, "driver": v.Driver, "name": v.Name,
			"fee_percent": feePercent, "enabled": v.Enabled, "sort": v.Sort,
		}
		for _, k := range adminGatewayConfigFields {
			if isAdminGatewaySecretField(k) {
				row["has_"+k] = strings.TrimSpace(v.Config[k]) != ""
				continue
			}
			row[k] = v.Config[k]
		}
		out = append(out, row)
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
	cfg := make(map[string]string, len(adminGatewayConfigFields)+1)
	for _, k := range adminGatewayConfigFields {
		cfg[k] = strings.TrimSpace(fv(k))
	}
	cfg["fee_percent"] = feePercent
	// 密钥类字段留空表示沿用旧值：编辑时后台不回传原文，用户无需重填。
	if old, oldErr := g.GwRepo.Get(r.Context(), code); oldErr == nil {
		for _, k := range adminGatewaySecretFields {
			if cfg[k] == "" {
				cfg[k] = old.Config[k]
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
