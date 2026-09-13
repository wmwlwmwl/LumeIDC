package handler

import (
	"context"
	"net/http"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// Deps 集中承载渲染相关的共享依赖，替代原先散布在包级变量里的
// sessionsStore/adminSessions/balanceRepo/siteSettingsRepo 与 Set* 注入。
// 各 handler 结构体匿名内嵌 *Deps，生产环境由 httpserver 组合根一次性注入。
type Deps struct {
	PageStore    *middleware.Store           // 用户侧/匿名会话 CSRF
	AdminStore   *middleware.Store           // 后台会话 CSRF
	Balance      *repo.Balance               // 导航栏余额（nil 时跳过余额注入）
	Settings     *repo.Settings              // 站点品牌信息（nil 时回退默认）
	AdminPathCfg *middleware.AdminPathConfig // 自定义后台路径（运行期可改，nil 时用默认 /admin）
}

// pageCSRF 取用户侧会话 CSRF；无会话时启动匿名会话（原地改写请求上下文，语义与旧 csrfOf 一致）。
func (d *Deps) pageCSRF(w http.ResponseWriter, r *http.Request) string {
	return csrfOf(d.PageStore, w, r)
}

// adminCSRF 取后台会话 CSRF。
func (d *Deps) adminCSRF(w http.ResponseWriter, r *http.Request) string {
	return csrfOf(d.AdminStore, w, r)
}

// currentSiteInfo 读取当前站点信息；Settings 未注入（安装期/测试）时回退默认。
func (d *Deps) currentSiteInfo() service.SiteInfo {
	if d == nil || d.Settings == nil {
		return service.SiteInfo{Name: service.DefaultSiteName}
	}
	return service.LoadSiteInfo(context.Background(), d.Settings)
}

// fillSiteData 将站点信息写入渲染数据（前台/后台/认证页共用）。
func (d *Deps) fillSiteData(data map[string]any) {
	if data == nil {
		return
	}
	si := d.currentSiteInfo()
	data["SiteName"] = si.Name
	data["SiteMark"] = siteFirstMark(si.Name)
	data["SiteDescription"] = si.Description
	data["SiteKeywords"] = si.Keywords
	data["ServiceEmail"] = si.ServiceEmail
	data["ServicePhone"] = si.ServicePhone
	data["ServiceHours"] = si.ServiceHours
}



