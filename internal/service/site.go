package service

import (
	"context"
	"encoding/json"
	"strings"

	"lumeidc/internal/repo"
)

// 站点信息设置键（settings 表）。
const (
	KeySiteName         = "site_name"
	KeySiteDescription  = "site_description"
	KeySiteKeywords     = "site_keywords"
	KeyServiceEmail     = "service_email"
	KeyServicePhone     = "service_phone"
	KeyServiceHours     = "service_hours"
	KeyServiceContacts  = "service_contacts"
	KeySiteURL          = "site_url"          // 站点对外地址（含协议），留空则按用户访问的请求自动推断
	KeyListenPort       = "listen_port"       // 监听端口，留空使用 config.yaml 的 listen
	KeyAdminPath        = "admin_path"        // 自定义后台访问路径（启动时读取，改后重启生效）
	KeyUpstreamTimezone = "upstream_timezone" // 上游面板时区（IANA 名，如 Asia/Shanghai），留空按本机时区解释上游无时区时间

	DefaultSiteName = "LumeIDC"
)

// SiteInfo 站点品牌/SEO/客服配置（页面渲染与邮件品牌统一来源）。
type SiteContact struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	Link  string `json:"link,omitempty"`
}

type SiteInfo struct {
	Name            string
	Description     string
	Keywords        string
	ServiceEmail    string
	ServicePhone    string
	ServiceHours    string
	ServiceContacts []SiteContact
}

// LoadSiteInfo 从 settings 读取站点信息；缺省回退默认值。
func LoadSiteInfo(ctx context.Context, s *repo.Settings) SiteInfo {
	defaults := map[string]string{
		KeySiteName: DefaultSiteName,
	}
	keys := []string{KeySiteName, KeySiteDescription, KeySiteKeywords, KeyServiceEmail, KeyServicePhone, KeyServiceHours, KeyServiceContacts}
	values := map[string]string{}
	if s != nil {
		if loaded, err := s.GetMany(ctx, keys...); err == nil {
			values = loaded
		}
	}
	read := func(key string) string {
		if v := strings.TrimSpace(values[key]); v != "" {
			return v
		}
		return defaults[key]
	}
	info := SiteInfo{
		Name:         read(KeySiteName),
		Description:  read(KeySiteDescription),
		Keywords:     read(KeySiteKeywords),
		ServiceEmail: read(KeyServiceEmail),
		ServicePhone: read(KeyServicePhone),
		ServiceHours: read(KeyServiceHours),
	}
	if raw := strings.TrimSpace(values[KeyServiceContacts]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info.ServiceContacts)
	}
	legacy := make([]SiteContact, 0, 3)
	hasType := func(kind string) bool {
		for _, contact := range info.ServiceContacts {
			if contact.Type == kind {
				return true
			}
		}
		return false
	}
	if info.ServiceEmail != "" && !hasType("email") {
		legacy = append(legacy, SiteContact{Type: "email", Name: "邮箱", Value: info.ServiceEmail, Link: "mailto:" + info.ServiceEmail})
	}
	if info.ServicePhone != "" && !hasType("phone") {
		legacy = append(legacy, SiteContact{Type: "phone", Name: "电话", Value: info.ServicePhone, Link: "tel:" + info.ServicePhone})
	}
	if info.ServiceHours != "" && !hasType("hours") {
		legacy = append(legacy, SiteContact{Type: "hours", Name: "服务时间", Value: info.ServiceHours})
	}
	info.ServiceContacts = append(legacy, info.ServiceContacts...)
	if strings.TrimSpace(info.Name) == "" {
		info.Name = DefaultSiteName
	}
	return info
}

// SiteName 返回站点名称（邮件/验证码主题用）。
func (n *Notifier) SiteName(ctx context.Context) string {
	if n == nil || n.Settings == nil {
		return DefaultSiteName
	}
	return LoadSiteInfo(ctx, n.Settings).Name
}
