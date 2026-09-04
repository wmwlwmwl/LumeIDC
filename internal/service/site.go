package service

import (
	"context"
	"strings"

	"lumeidc/internal/repo"
)

// 站点信息设置键（settings 表）。
const (
	KeySiteName        = "site_name"
	KeySiteDescription = "site_description"
	KeySiteKeywords    = "site_keywords"
	KeyServiceEmail    = "service_email"
	KeyServicePhone    = "service_phone"
	KeyServiceHours    = "service_hours"
	KeySiteURL         = "site_url"    // 站点对外地址（含协议），留空则按用户访问的请求自动推断
	KeyListenPort      = "listen_port" // 监听端口，留空使用 config.yaml 的 listen
	KeyAdminPath       = "admin_path"  // 自定义后台访问路径（启动时读取，改后重启生效）

	DefaultSiteName = "LumeIDC"
)

// SiteInfo 站点品牌/SEO/客服配置（页面渲染与邮件品牌统一来源）。
type SiteInfo struct {
	Name         string
	Description  string
	Keywords     string
	ServiceEmail string
	ServicePhone string
	ServiceHours string
}

// LoadSiteInfo 从 settings 读取站点信息；缺省回退默认值。
func LoadSiteInfo(ctx context.Context, s *repo.Settings) SiteInfo {
	defaults := map[string]string{
		KeySiteName: DefaultSiteName,
	}
	keys := []string{KeySiteName, KeySiteDescription, KeySiteKeywords, KeyServiceEmail, KeyServicePhone, KeyServiceHours}
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
