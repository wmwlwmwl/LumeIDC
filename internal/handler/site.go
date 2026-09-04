package handler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
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

// siteBaseURL 返回站点地址前缀（不含末尾 /）：后台 site_url 优先，
// 未配置时按当前请求动态推断（反代须透传 X-Forwarded-Proto）。
func siteBaseURL(ctx context.Context, s *repo.Settings, r *http.Request) string {
	if s != nil {
		if v, err := s.Get(ctx, service.KeySiteURL); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return strings.TrimRight(v, "/")
			}
		}
	}
	return inferBaseFromRequest(r)
}

// inferBaseFromRequest 从当前请求推断协议与主机（HTTPS 直连或反代标记）。
func inferBaseFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// validSiteURL 校验站点地址：必须 http(s):// 开头且不含路径与查询。
func validSiteURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
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
		service.KeySiteURL:         get(service.KeySiteURL),
		service.KeyListenPort:      get(service.KeyListenPort),
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
	siteURL := strings.TrimRight(strings.TrimSpace(r.PostFormValue(service.KeySiteURL)), "/")
	if siteURL != "" && !validSiteURL(siteURL) {
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("站点地址无效：需以 http:// 或 https:// 开头且不含路径（可留空自动推断）"), http.StatusSeeOther)
		return
	}
	port := strings.TrimSpace(r.PostFormValue(service.KeyListenPort))
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("监听端口无效：需为 1-65535 的整数（留空使用 config.yaml 的 listen）"), http.StatusSeeOther)
			return
		}
	} else if a.ListenSwitcher != nil {
		// 留空回退 config.yaml 的 listen；缺省用 :8080。
		def := a.DefaultListen
		if def == "" {
			def = ":8080"
		}
		if strings.HasPrefix(def, ":") && def != ":" {
			if n, err := strconv.Atoi(def[1:]); err == nil && n >= 1 && n <= 65535 {
				port = def[1:]
			}
		}
	}
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
	set(service.KeySiteURL, siteURL)
	// 监听端口热切换：先绑定成功（旧服务不中断）再落库。
	addr := a.DefaultListen
	if addr == "" {
		addr = ":8080"
	}
	if port != "" {
		addr = ":" + port
	}
	if a.ListenSwitcher != nil {
		if err := a.ListenSwitcher(addr); err != nil {
			http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("切换监听端口失败（"+err.Error()+"），设置未保存"), http.StatusSeeOther)
			return
		}
	}
	set(service.KeyListenPort, port)
	set(service.KeyAdminPath, adminPath)
	if a.AdminPathCfg != nil {
		a.AdminPathCfg.Set(adminPath) // 立即生效，无需重启
	}
	// 端口热切换后，旧端口正在优雅关闭：若用户显式带端口访问（如 yun.662662.xyz:9090），
	// 跳转地址必须带上新端口，否则 303 落回已关闭的旧端口；域名直连（无端口、走反代）保持相对跳转。
	// 反代透传的内网/回环主机（如 127.0.0.1:8080）不能作为跳转主机，否则浏览器会跳向内网地址。
	target := "/admin/site?ok=1"
	if port != "" {
		if host, _, err := net.SplitHostPort(r.Host); err == nil && publicHost(host) {
			target = "//" + net.JoinHostPort(host, port) + "/admin/site?ok=1"
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// publicHost 报告主机名是否可作为对外跳转目标：
// localhost、回环/私网 IP（127.x、10.x、192.168.x、172.16-31.x、169.254.x 等）均视为不可见，返回 false。
// 纯公网 IP 或域名返回 true（域名不做解析验证）。
func publicHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // 域名
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}
