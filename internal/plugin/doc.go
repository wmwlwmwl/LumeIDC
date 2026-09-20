// Package plugin 是 LumeIDC 的编译期插件框架。
//
// 完整的插件开发指南见 docs/plugin.md（配置/事件/路由/页面/cron/启停语义）。
//
// # 定位
//
// 新功能收敛为独立目录，核心代码零改动；插件与核心同仓库、同构建，
// 仍发布单二进制。插件是一等公民代码（同评审、同权限），不做沙箱。
//
// # 双轨模型
//
// 接口类（单能力：支付网关/上游供应商/短信/验证码/实名）走各领域注册表
// init() 自注册（gateway.Register、server.Register 等），不实现本接口。
// 业务类（带事件订阅/路由/建表/页面的完整功能）实现 Plugin 接口：
//
//	type MyPlugin struct{ host *plugin.Host }
//
//	func (p *MyPlugin) Info() plugin.Info {
//		return plugin.Info{Name: "myplugin", Title: "我的插件", Version: "1.0.0", Description: "..."}
//	}
//
//	func (p *MyPlugin) Init(h *plugin.Host) error {
//		p.host = h
//		h.Subscribe(plugin.EventOrderPaid, p.onOrderPaid)
//		return nil
//	}
//
//	func init() { plugin.Register(&MyPlugin{}) }
//
// # 目录约定
//
//	internal/plugins/{name}/        插件包（Name 与目录同名，小写蛇形）
//	internal/plugins/{name}/migrations/*.sql   可选建表（实现 Migrator）
//	internal/plugins/all/all.go     聚合 blank import——新增插件唯一要加的一行
//	web/src/plugins/{name}/Admin.vue           可选后台页（adminRegistry 加一行映射）
//	web/src/plugins/registry.ts                前端聚合（admin/client 双注册表）
//
// plugins/ 目录同时驻留接口类供应商实现（zjmf、easypanel）：它们不是业务插件
// （不调 plugin.Register、不出现在插件管理页），仅在 init() 中 server.Register
// 自注册到供应商注册表。判别方式：看 init 注册到哪个注册表。
//
// # 可选能力（类型断言探测，与 gateway.OrderQuerier 同风格）
//
//	Migrator               启动时执行插件迁移（先于 Init，前缀 plugin/{name}/）
//	AdminRouteRegistrar    管理 API：子 mux 相对路径，挂 /admin/plugin/{name}/ 前缀
//	ClientRouteRegistrar   用户侧 API：挂 /plugin/{name}/ 前缀
//	ConfigSchemaProvider   声明配置项 → 框架自动提供 config API 与后台配置表单页
//	AdminMenuProvider      后台侧栏菜单项（/admin#/plugin/{name} 槽位；
//	                       MenuItem.Parent 选业务分组——MenuGroupOps/Business/Users/
//	                       Settings/System，空则收拢进「插件」分组）
//	ClientPageProvider     前台用户中心菜单项（MenuItem.To 可自定义存量路径）
//	AdminWidgetProvider    后台首页挂件（JSON 卡片，前端统一渲染）
//	CronContributor        定时任务（与核心任务同待遇：失败管理员告警）
//
// # 生命周期
//
// 插件表（plugins）记录启用态，后台「插件管理」页即时启停（软禁用，无需重启）：
// 禁用 = 事件/过滤器不投递、路由 404、菜单/清单不显示、cron 不触发；
// 迁移与数据不受影响。缺省启用。
//
// # 事件与过滤器
//
// 核心在关键流程埋点（事件常量见 events.go，EventCatalog 含中文标签）；
// 插件经 Host.Subscribe 订阅（"*" 通配全部事件，EventName(ctx) 取当前事件名），
// Emit 同步广播、单订阅者 panic/错误仅记日志不阻断主流程。
// 过滤器（Host.SubscribeFilter + ApplyFilters）用于可改数据的场景
// （如 FilterNotifyMessage 通知发送前改写标题/正文）：链式执行，单个出错保留当前值。
// 插件可 RegisterEvent 注册自定义事件供其他插件/订阅方发现。
//
// # 插件内常用 helper
//
//	plugin.AdminOK / plugin.AdminSession   管理鉴权
//	plugin.RequireUserID                    用户登录校验
//	plugin.WriteJSON / JSONFail / StatusFail 响应输出（与核心约定一致）
//	host.Config(ctx, key)                   读插件配置（settings 表 plugin.{name}.{key}）
//	host.Notify.NotifyTemplate(...)         发站内信/邮件（走核心通知系统）
//	host.PrivateDir()                       插件私有存储目录（需自 mkdir）
//
// # 故障策略
//
// 启动期严格：Init 失败 → 启动失败（编译期插件的问题应尽早暴露）。
// 运行期宽容：事件 handler 崩溃 → 隔离记日志（插件崩不影响核心）。
package plugin
