package handler

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
// ponytail: site_url 未配置时只能信任请求主机，而 Host 由客户端控制——它可把支付回调/回跳
// 地址指到任意域。此处只做语法收敛（拒绝空、带路径/凭据、含空白或控制字符的主机），
// 返回空串表示"推断不出可用地址"；真要根治需强制后台配置 site_url（见调用方的兜底提示）。
func inferBaseFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if !validHostHeader(host) {
		return ""
	}
	return scheme + "://" + host
}

// validHostHeader 校验主机可安全拼进 URL：不含空白/控制字符，也不含会改变 URL 语义的
// / \ ? # @（这些字符会让 "scheme://host" 之后的路径被劫持到别处）。
func validHostHeader(host string) bool {
	if host == "" || len(host) > 255 {
		return false
	}
	for _, c := range host {
		if c <= ' ' || c == 0x7f || c == '/' || c == '\\' || c == '?' || c == '#' || c == '@' {
			return false
		}
	}
	return true
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
	siteInfo := service.LoadSiteInfo(r.Context(), s)
	contactsJSON, _ := json.Marshal(siteInfo.ServiceContacts)
	cfg := map[string]string{
		service.KeySiteName:         get(service.KeySiteName),
		service.KeySiteDescription:  get(service.KeySiteDescription),
		service.KeySiteKeywords:     get(service.KeySiteKeywords),
		service.KeyServiceEmail:     get(service.KeyServiceEmail),
		service.KeyServicePhone:     get(service.KeyServicePhone),
		service.KeyServiceHours:     get(service.KeyServiceHours),
		service.KeyServiceContacts:  string(contactsJSON),
		service.KeySiteURL:          get(service.KeySiteURL),
		service.KeyListenPort:       get(service.KeyListenPort),
		service.KeyAdminPath:        get(service.KeyAdminPath),
		service.KeyUpstreamTimezone: get(service.KeyUpstreamTimezone),
	}
	out := map[string]any{"ok": 1}
	for k, v := range cfg {
		out[k] = v
	}
	writeJSON(w, out)
}

// adminSiteSave POST /admin/site — 保存站点信息。
func (a *Admin) adminSiteSave(w http.ResponseWriter, r *http.Request) {
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
	if vals == nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/site?err="+url.QueryEscape("表单解析失败"), http.StatusSeeOther)
			return
		}
	}
	fail := func(msg string) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/admin/site?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	name := strings.TrimSpace(fv(service.KeySiteName))
	if name == "" {
		fail("站点名称不能为空")
		return
	}
	desc := strings.TrimSpace(fv(service.KeySiteDescription))
	keywords := strings.TrimSpace(fv(service.KeySiteKeywords))
	email := strings.TrimSpace(fv(service.KeyServiceEmail))
	phone := strings.TrimSpace(fv(service.KeyServicePhone))
	hours := strings.TrimSpace(fv(service.KeyServiceHours))
	contactsRaw := strings.TrimSpace(fv(service.KeyServiceContacts))
	var contacts []service.SiteContact
	if contactsRaw != "" {
		if err := json.Unmarshal([]byte(contactsRaw), &contacts); err != nil {
			fail("联系方式格式无效")
			return
		}
		if len(contacts) > 20 {
			fail("联系方式最多 20 条")
			return
		}
		for i := range contacts {
			contacts[i].Type = strings.TrimSpace(contacts[i].Type)
			contacts[i].Name = strings.TrimSpace(contacts[i].Name)
			contacts[i].Value = strings.TrimSpace(contacts[i].Value)
			contacts[i].Link = strings.TrimSpace(contacts[i].Link)
			if contacts[i].Type == "email" {
				contacts[i].Link = "mailto:" + contacts[i].Value
			} else if contacts[i].Type == "phone" {
				contacts[i].Link = "tel:" + contacts[i].Value
			}
			if contacts[i].Type == "" || contacts[i].Name == "" || contacts[i].Value == "" {
				fail("每条联系方式都需要填写类型、名称和内容")
				return
			}
			if len([]rune(contacts[i].Name)) > 32 || len([]rune(contacts[i].Value)) > 256 || len([]rune(contacts[i].Link)) > 512 {
				fail("联系方式内容过长")
				return
			}
			if contacts[i].Link != "" {
				u, err := url.Parse(contacts[i].Link)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto" && u.Scheme != "tel") {
					fail("联系方式链接仅支持 http、https、mailto 或 tel")
					return
				}
			}
		}
	}
	if contactsRaw == "" {
		contacts = append(contacts,
			service.SiteContact{Type: "email", Name: "邮箱", Value: email, Link: "mailto:" + email},
			service.SiteContact{Type: "phone", Name: "电话", Value: phone, Link: "tel:" + phone},
			service.SiteContact{Type: "hours", Name: "服务时间", Value: hours},
		)
	}
	siteURL := strings.TrimRight(strings.TrimSpace(fv(service.KeySiteURL)), "/")
	if siteURL != "" && !validSiteURL(siteURL) {
		fail("站点地址无效：需以 http:// 或 https:// 开头且不含路径（可留空自动推断）")
		return
	}
	port := strings.TrimSpace(fv(service.KeyListenPort))
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			fail("监听端口无效：需为 1-65535 的整数（留空使用 config.yaml 的 listen）")
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
	adminPath := strings.TrimSpace(fv(service.KeyAdminPath))
	if adminPath != "" && !middleware.ValidAdminPath(adminPath) {
		fail("后台路径无效：仅允许 /字母数字_-，且不能与公共路径（如 /login、/services）冲突")
		return
	}
	upstreamTZ := strings.TrimSpace(fv(service.KeyUpstreamTimezone))
	if upstreamTZ != "" {
		if _, err := time.LoadLocation(upstreamTZ); err != nil {
			fail("上游时区无效：需为 IANA 时区名（如 Asia/Shanghai），或留空使用本机时区")
			return
		}
	}
	limit := 512
	if len([]rune(name)) > 128 {
		fail("站点名称过长")
		return
	}
	if len([]rune(desc)) > limit || len([]rune(keywords)) > limit || len([]rune(email)) > 128 || len([]rune(phone)) > 64 || len([]rune(hours)) > 128 {
		fail("字段过长")
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
	storedContacts, _ := json.Marshal(contacts)
	set(service.KeyServiceHours, hours)
	set(service.KeyServiceContacts, string(storedContacts))
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
			fail("切换监听端口失败（" + err.Error() + "），设置未保存")
			return
		}
	}
	set(service.KeyListenPort, port)
	set(service.KeyAdminPath, adminPath)
	set(service.KeyUpstreamTimezone, upstreamTZ)
	if a.AdminPathCfg != nil {
		a.AdminPathCfg.Set(adminPath) // 立即生效，无需重启
	}
	newPath := strings.Trim(adminPath, "/")
	if newPath == "" {
		newPath = "admin"
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{
			"ok": 1, "admin_path": "/" + newPath, "listen_port": port,
		})
		return
	}
	// 端口热切换后，旧端口正在优雅关闭：若用户显式带端口访问（如 yun.662662.xyz:9090），
	// 跳转地址必须带上新端口，否则 303 落回已关闭的旧端口；域名直连（无端口、走反代）保持相对跳转。
	// 后台路径跳转直接按新 admin_path 构造：修改路径后旧前缀立即被屏蔽，不能依赖 middleware 改写。
	target := "/" + newPath + "/site?ok=1"
	if port != "" {
		if host, _, err := net.SplitHostPort(r.Host); err == nil && publicHost(host) {
			target = "//" + net.JoinHostPort(host, port) + target
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
