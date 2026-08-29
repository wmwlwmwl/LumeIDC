package server

import (
	"context"
	"html/template"
)

// WidgetData 详情页专属区块渲染所需的基础数据（由调用方注入，密码已解密）。
type WidgetData struct {
	ServiceID  int64
	CSRF       string       // 全局控制台路由表单令牌（重置密码等复用 /services/{id}/console）
	Password   string       // 实例密码（password_crypt 解密；无则空）
	StatusText string       // 本地服务状态文本（激活/已停机…）
	Overview   HostOverview // 本次详情请求已获取的上游概况，避免 provider 重复请求
}

// DetailWidgetProvider 可选：供应商自带服务详情页管理区块（独立 UI，插槽注入，
// 参照 FOSSBilling 模块 Widget 机制）。实现方内嵌自己的模板，返回渲染后的 HTML；
// 失败时应返回空并由调用方静默降级到全局面板。
// 实现了该接口的供应商：详情页的"实例控制台/登录信息"全局面板会让位给专属区块。
type DetailWidgetProvider interface {
	DetailWidget(ctx context.Context, cfg Config, upstreamHostID int64, data WidgetData) (template.HTML, error)
}
