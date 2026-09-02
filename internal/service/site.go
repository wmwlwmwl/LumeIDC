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
	read := func(key, def string) string {
		if s == nil {
			return def
		}
		v, err := s.Get(ctx, key)
		if err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	info := SiteInfo{
		Name:         read(KeySiteName, DefaultSiteName),
		Description:  read(KeySiteDescription, ""),
		Keywords:     read(KeySiteKeywords, ""),
		ServiceEmail: read(KeyServiceEmail, ""),
		ServicePhone: read(KeyServicePhone, ""),
		ServiceHours: read(KeyServiceHours, ""),
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
