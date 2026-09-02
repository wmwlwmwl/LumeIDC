package handler

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// siteSettingsRepo 由 httpserver 注入（与 balanceRepo 同模式），供全站品牌注入。
var siteSettingsRepo *repo.Settings

// SetSiteRepo 注入站点配置仓库。
func SetSiteRepo(s *repo.Settings) { siteSettingsRepo = s }

// currentSiteInfo 读取当前站点信息；未初始化（安装期）回退默认。
func currentSiteInfo() service.SiteInfo {
	if siteSettingsRepo == nil {
		return service.SiteInfo{Name: service.DefaultSiteName}
	}
	return service.LoadSiteInfo(context.Background(), siteSettingsRepo)
}

// siteFirstMark 取站点名称首字符作为 Logo 占位字母。
func siteFirstMark(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return "L"
	}
	r, _ := utf8.DecodeRuneInString(s)
	return string(r)
}

// fillSiteData 将站点信息写入任意 map 渲染数据（前台/认证页共用）。
func fillSiteData(data map[string]any) {
	if data == nil {
		return
	}
	si := currentSiteInfo()
	data["SiteName"] = si.Name
	data["SiteMark"] = siteFirstMark(si.Name)
	data["SiteDescription"] = si.Description
	data["SiteKeywords"] = si.Keywords
	data["ServiceEmail"] = si.ServiceEmail
	data["ServicePhone"] = si.ServicePhone
	data["ServiceHours"] = si.ServiceHours
}

// adminSite GET /admin/site — 站点设置页。
func (a *Admin) adminSite(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	s := &repo.Settings{DB: a.DB}
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
	}
	renderAdmin(w, "admin_site.html", AdminData{
		CSRF:        csrfOf(adminSessions, w, r),
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
	limit := 512
	if len([]rune(name)) > 128 {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("站点名称过长"), http.StatusSeeOther)
		return
	}
	if len([]rune(desc)) > limit || len([]rune(keywords)) > limit || len([]rune(email)) > 128 || len([]rune(phone)) > 64 || len([]rune(hours)) > 128 {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("字段过长"), http.StatusSeeOther)
		return
	}
	s := &repo.Settings{DB: a.DB}
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
	http.Redirect(w, r, "/admin/site?ok=1", http.StatusSeeOther)
}
