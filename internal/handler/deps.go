package handler

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"sync"
	texttemplate "text/template"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// Deps 集中承载渲染相关的共享依赖，替代原先散布在包级变量里的
// sessionsStore/adminSessions/balanceRepo/siteSettingsRepo 与 Set* 注入。
// 各 handler 结构体匿名内嵌 *Deps，生产环境由 httpserver 组合根一次性注入。
type Deps struct {
	PageStore  *middleware.Store // 用户侧/匿名会话 CSRF
	AdminStore *middleware.Store // 后台会话 CSRF
	Balance    *repo.Balance     // 导航栏余额（nil 时跳过余额注入）
	Settings   *repo.Settings    // 站点品牌信息（nil 时回退默认）

	// 模板懒缓存：首次按页解析，之后复用（html/template 解析后并发执行安全）。
	tplMu  sync.Mutex
	pageT  map[string]*template.Template // 前台布局页（site.html + 页面）
	adminT map[string]*template.Template // 后台布局页（admin.html + 页面）
	authT  *texttemplate.Template        // 认证页（auth.html，保持 text/template 语义）
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

// siteTemplate 按页返回解析好的前台模板（site.html 布局 + 页面），首次解析后缓存。
func (d *Deps) siteTemplate(page string) (*template.Template, error) {
	d.tplMu.Lock()
	defer d.tplMu.Unlock()
	if d.pageT == nil {
		d.pageT = map[string]*template.Template{}
	}
	if t, ok := d.pageT[page]; ok {
		return t, nil
	}
	t, err := template.New(page).Funcs(template.FuncMap{"safeDescriptionHTML": safeDescriptionHTML}).ParseFS(siteFS, "templates/site.html", "templates/"+page)
	if err != nil {
		return nil, err
	}
	d.pageT[page] = t
	return t, nil
}

// adminTemplate 按页返回解析好的后台模板，首次解析后缓存。
func (d *Deps) adminTemplate(page string) (*template.Template, error) {
	d.tplMu.Lock()
	defer d.tplMu.Unlock()
	if d.adminT == nil {
		d.adminT = map[string]*template.Template{}
	}
	if t, ok := d.adminT[page]; ok {
		return t, nil
	}
	t, err := template.ParseFS(adminFS, "templates/admin.html", "templates/"+page)
	if err != nil {
		return nil, err
	}
	d.adminT[page] = t
	return t, nil
}

// authTemplate 返回解析好的认证页模板，首次解析后缓存。
func (d *Deps) authTemplate() (*texttemplate.Template, error) {
	d.tplMu.Lock()
	defer d.tplMu.Unlock()
	if d.authT == nil {
		t, err := texttemplate.ParseFS(authFS, "templates/auth.html")
		if err != nil {
			return nil, err
		}
		d.authT = t
	}
	return d.authT, nil
}

// render 用 site.html 作为布局渲染前台子页。
func (d *Deps) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	tpl, err := d.siteTemplate(page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	d.fillSiteData(data)
	data["Page"] = page
	data["PageTitle"] = pageTitleLabel(page)
	if sess := middleware.FromSession(r.Context()); sess != nil {
		data["CSRF"] = sess.CSRFToken()
		if sess.UserID > 0 && !sess.IsAdmin {
			data["LoggedIn"] = true
			if d != nil && d.Balance != nil {
				if bal, err := d.Balance.Get(r.Context(), sess.UserID); err == nil {
					data["Balance"] = bal
				}
			}
		}
	}
	if err := tpl.ExecuteTemplate(w, "site", data); err != nil {
		log.Printf("[template] %s: %v", page, err)
	}
}

// renderAdmin 用 admin.html 作为布局渲染后台页面。
func (d *Deps) renderAdmin(w http.ResponseWriter, page string, data AdminData) {
	data.Page = page
	si := d.currentSiteInfo()
	data.SiteName = si.Name
	data.SiteMark = siteFirstMark(si.Name)
	tpl, err := d.adminTemplate(page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// 直接传结构体：此前手动拼 map 曾漏字段（Providers/Secret/TotalProfit 静默丢失），勿再回退。
	if err := tpl.ExecuteTemplate(w, "admin", data); err != nil {
		log.Printf("[template] %s: %v", page, err)
	}
}

// renderAuth 渲染登录/注册认证页（模板使用 text/template，不自动转义，语义与旧 renderAuth 一致）。
func (d *Deps) renderAuth(w http.ResponseWriter, data map[string]any) {
	d.fillSiteData(data)
	tpl, err := d.authTemplate()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tpl.Execute(w, data); err != nil {
		log.Printf("[template] auth.html 执行失败: %v", err)
	}
}
