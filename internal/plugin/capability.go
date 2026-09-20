package plugin

import (
	"context"
	"io/fs"
	"net/http"
)

// 可选能力接口：插件按需实现，组合根以类型断言探测
// （与 gateway.OrderQuerier、server.ProductFormProvider 同风格）。

// Migrator 可选能力：插件提供 SQL 迁移（embed FS，内含 migrations/*.sql），
// 启动时以 "plugin/{插件名}/" 前缀独立记录 version，先于 Init 执行。
type Migrator interface {
	Migrations() fs.FS
}

// AdminRouteRegistrar 可选能力：注册插件管理 API。组合根把子 mux 挂到
// /admin/plugin/{name}/ 前缀（插件内写相对路径，如 "GET /config"），
// 统一包启用态闸门（禁用即 404）；管理鉴权由插件 handler 内自行完成
// （middleware.FromSession(r.Context()).IsAdmin）。
type AdminRouteRegistrar interface {
	RegisterAdminRoutes(mux *http.ServeMux)
}

// ClientRouteRegistrar 可选能力：注册用户侧 API，挂 /plugin/{name}/ 前缀，
// 同样带启用态闸门。
type ClientRouteRegistrar interface {
	RegisterClientRoutes(mux *http.ServeMux)
}

// Gate 插件路由启用态闸门：插件禁用时一律 404（组合根挂载插件路由时使用）。
func Gate(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Enabled(name) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminMenuProvider 可选能力：向后台侧栏贡献菜单项，
// 路径约定为 /plugin/{name}（前端 PluginShell 按名渲染插件页）。
type AdminMenuProvider interface {
	AdminMenu() MenuItem
}

// ClientPageProvider 可选能力：向前台用户中心贡献页面，
// 前端槽位路由 /plugin/{name}（ClientShell 渲染 clientRegistry 中同名组件）。
type ClientPageProvider interface {
	ClientPage() MenuItem
}

// 后台菜单业务分组标识（AdminMenu().Parent 取值；空 = 收拢进「插件」分组）。
// 对齐魔方 v10：插件菜单可挂进对应业务分类，而非全部堆在插件分组。
const (
	MenuGroupOps      = "ops"      // 运营概览
	MenuGroupBusiness = "business" // 业务管理
	MenuGroupUsers    = "users"    // 用户与内容
	MenuGroupSettings = "settings" // 设置
	MenuGroupSystem   = "system"   // 系统设置
)

// MenuItem 菜单项。Icon 为 remixicon 标识，如 "ri:puzzle-line"。
// To 为自定义跳转路径（前台菜单用；空 = 默认 /plugin/{name} 槽位页）。
// Parent 为后台菜单归属（MenuGroupXxx 常量；空/未知 = 「插件」分组）。
// 后台插件页固定走 /plugin/{name} 槽位，不读 To。
type MenuItem struct {
	Title  string
	Icon   string
	To     string
	Parent string
}

// ---- 配置 schema 自动渲染 ----

// ConfigOption 下拉/多选的选项（数组保序，前端按声明顺序渲染）。
type ConfigOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConfigField 配置项声明。实现 ConfigSchemaProvider 后，框架自动提供
// /admin/plugin/{name}/config 读写 API 与后台配置表单页，插件经 Host.Config 读取。
type ConfigField struct {
	Key     string         `json:"key"`
	Title   string         `json:"title"`
	Type    string         `json:"type"` // text|number|password|switch|select|multiselect|textarea
	Options []ConfigOption `json:"options,omitempty"` // select/multiselect 静态选项
	// OptionsRef 动态选项引用。目前支持 "plugin.events"：取事件目录（EventCatalog）。
	OptionsRef string `json:"optionsRef,omitempty"`
	Tip        string `json:"tip,omitempty"`
	Default    string `json:"default,omitempty"`
}

// ConfigSchemaProvider 可选能力：声明配置项。存储走 settings 表
// （键 plugin.{name}.{key}；multiselect 存 JSON 数组字符串；password 不回显）。
type ConfigSchemaProvider interface {
	ConfigSchema() []ConfigField
}

// ---- cron 任务贡献 ----

// CronJob 插件定时任务。与核心任务同待遇：失败经管理员告警（按 Name+日期去重）。
type CronJob struct {
	Name string // 唯一名（告警去重键）
	Spec string // robfig cron 表达式，如 "@every 10m"、"@daily"
	What string // 中文任务名（告警文案）
	Run  func(ctx context.Context) error
}

// CronContributor 可选能力：注册插件定时任务。插件禁用时任务不触发。
type CronContributor interface {
	CronJobs() []CronJob
}

// ---- 后台首页挂件 ----

// WidgetRow 挂件行（标签 + 值 + 可选后台跳转路径）。
type WidgetRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
	To    string `json:"to,omitempty"`
}

// WidgetData 挂件数据。前端统一渲染为卡片——不注入插件 HTML（安全边界）。
type WidgetData struct {
	Title string      `json:"title"`
	Icon  string      `json:"icon"`
	Rows  []WidgetRow `json:"rows"`
}

// AdminWidgetProvider 可选能力：向后台首页贡献挂件。
// 实现应 best-effort：取数失败返回 error 即可，该挂件跳过不显示。
type AdminWidgetProvider interface {
	AdminWidget(ctx context.Context) (*WidgetData, error)
}

// ---- 前台内容注入 ----

// ClientInjection 前台注入片段（对应魔方 template 输出钩子：统计代码、客服悬浮窗等）。
// Position: "head"（注入 document.head）| "body_bottom"（页面尾部渲染）。
// 安全约定：内容为站点主自装插件代码，信任级等同核心模板——不过滤、不转义。
type ClientInjection struct {
	Position string `json:"position"`
	HTML     string `json:"html"`
}

// ClientInjectionProvider 可选能力：向前台页面注入内容片段。
// 经 GET /plugins/injections 公开输出（登录页也需生效）；插件禁用时自动停止注入。
type ClientInjectionProvider interface {
	ClientInjections() []ClientInjection
}
