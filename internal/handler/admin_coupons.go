package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (a *Admin) adminCoupons(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	list, err := a.Coupons.List(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	a.renderAdmin(w, "admin_coupons.html", AdminData{
		Rows:  list,
		CSRF:  a.adminCSRF(w, r),
		Error: r.URL.Query().Get("err"),
		Msg:   r.URL.Query().Get("ok"),
	})
}

// adminSettings GET /admin/settings — 通知/SMTP 配置。

func (a *Admin) adminCouponCreate(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	typ := r.PostFormValue("type")
	value, _ := strconv.ParseFloat(r.PostFormValue("value"), 64)
	minA, _ := strconv.ParseFloat(r.PostFormValue("min_amount"), 64)
	limit, _ := strconv.Atoi(r.PostFormValue("usage_limit"))
	var expires *time.Time
	if es := strings.TrimSpace(r.PostFormValue("expires_at")); es != "" {
		if t, err := time.Parse("2006-01-02", es); err == nil {
			expires = &t
		}
	}
	if err := a.Coupons.Create(r.Context(), code, typ, value, minA, limit, expires); err != nil {
		http.Redirect(w, r, "/admin/coupons?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
}

// adminTestEmail POST /admin/settings/test-email — 用已保存的 SMTP 配置发送一封测试邮件。
