package handler

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

// siteFirstMark 取站点名称首字符作为 Logo 占位字母。
func siteFirstMark(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return "L"
	}
	r, _ := utf8.DecodeRuneInString(s)
	return string(r)
}

// adminSite GET /admin/site — 站点设置页。
func (a *Admin) adminSite(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	s := a.Settings
	get := func(k string) string {
		v, _ := s.Get(r.Context(), k)
		return v
	}
	cfg := map[string]string{
		service.KeySiteName:        get(service.KeySiteName),
		service.KeySiteDescription: get(service.KeySiteDescription),
		service.KeySiteKeywords:    get(service.KeySiteKeywords),
		service.KeyServiceEmail:    get(service.KeyServiceEmail),
		service.KeyServicePhone:    get(service.KeyServicePhone),
		service.KeyServiceHours:    get(service.KeyServiceHours),
		service.KeyAdminPath:       get(service.KeyAdminPath),
	}
	a.renderAdmin(w, "admin_site.html", AdminData{
		CSRF:        a.adminCSRF(w, r),
		Error:       r.URL.Query().Get("err"),
		Msg:         r.URL.Query().Get("ok"),
		ServersList: cfg,
	})
}

// adminSiteSave POST /admin/site — 保存站点信息。
func (a *Admin) adminSiteSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("表单解析失败"), http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.PostFormValue(service.KeySiteName))
	if name == "" {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("站点名称不能为空"), http.StatusSeeOther)
		return
	}
	desc := strings.TrimSpace(r.PostFormValue(service.KeySiteDescription))
	keywords := strings.TrimSpace(r.PostFormValue(service.KeySiteKeywords))
	email := strings.TrimSpace(r.PostFormValue(service.KeyServiceEmail))
	phone := strings.TrimSpace(r.PostFormValue(service.KeyServicePhone))
	hours := strings.TrimSpace(r.PostFormValue(service.KeyServiceHours))
	adminPath := strings.TrimSpace(r.PostFormValue(service.KeyAdminPath))
	if adminPath != "" && !middleware.ValidAdminPath(adminPath) {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("后台路径无效：仅允许 /字母数字_-，且不能与公共路径（如 /login、/services）冲突"), http.StatusSeeOther)
		return
	}
	limit := 512
	if len([]rune(name)) > 128 {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("站点名称过长"), http.StatusSeeOther)
		return
	}
	if len([]rune(desc)) > limit || len([]rune(keywords)) > limit || len([]rune(email)) > 128 || len([]rune(phone)) > 64 || len([]rune(hours)) > 128 {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("字段过长"), http.StatusSeeOther)
		return
	}
	s := a.Settings
	set := func(k, v string) {
		if err := s.Set(r.Context(), k, v); err != nil {
			http.Error(w, "保存站点信息失败", 500)
		}
	}
	set(service.KeySiteName, name)
	set(service.KeySiteDescription, desc)
	set(service.KeySiteKeywords, keywords)
	set(service.KeyServiceEmail, email)
	set(service.KeyServicePhone, phone)
	set(service.KeyServiceHours, hours)
	set(service.KeyAdminPath, adminPath)
	if a.AdminPathCfg != nil {
		a.AdminPathCfg.Set(adminPath) // 立即生效，无需重启
	}
	http.Redirect(w, r, "/admin/site?ok=1", http.StatusSeeOther)
}
