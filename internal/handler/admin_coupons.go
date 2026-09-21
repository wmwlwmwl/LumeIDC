package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

func (a *Admin) adminCoupons(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	list, err := a.Coupons.List(r.Context())
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		var exp string
		if c.ExpiresAt != nil {
			exp = c.ExpiresAt.Format("2006-01-02")
		}
		out = append(out, map[string]any{
			"id": c.ID, "code": c.Code, "type": c.Type, "value": c.Value,
			"min_amount": c.MinAmount, "expires_at": exp, "usage_limit": c.UsageLimit,
			"used": c.UsedCount, "active": c.Active,
			"apply_scope": c.ApplyScope, "recurring": c.Recurring, "need_product_ids": c.NeedProductIDs,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// adminSettings GET /admin/settings — 通知/SMTP 配置。

func (a *Admin) adminCouponCreate(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	code := strings.TrimSpace(fv("code"))
	typ := fv("type")
	value, _ := strconv.ParseFloat(fv("value"), 64)
	minA, _ := strconv.ParseFloat(fv("min_amount"), 64)
	limit, _ := strconv.Atoi(fv("usage_limit"))
	// 场景化（080）：适用范围 / 循环期数 / 需求商品
	opts := repo.CouponOptions{
		ApplyScope: strings.TrimSpace(fv("apply_scope")),
	}
	if v, err := strconv.Atoi(strings.TrimSpace(fv("recurring"))); err == nil {
		opts.Recurring = v
	}
	if raw := strings.TrimSpace(fv("need_product_ids")); raw != "" && raw != "[]" {
		var ids []int64
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			jsonStatus(w, r, 400, "需求商品格式无效")
			return
		}
		opts.NeedProductIDs = ids
	}
	var expires *time.Time
	if es := strings.TrimSpace(fv("expires_at")); es != "" {
		if t, err := time.Parse("2006-01-02", es); err == nil {
			expires = &t
		}
	}
	if err := a.Coupons.CreateWithOptions(r.Context(), code, typ, value, minA, limit, expires, opts); err != nil {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/coupons?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已创建"})
		return
	}
	http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
}

// adminTestEmail POST /admin/settings/test-email — 用已保存的 SMTP 配置发送一封测试邮件。
