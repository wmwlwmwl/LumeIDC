// Package dailyreport 每日运营报告插件：每天定时汇总昨日经营数据
// （新订单/支付金额/新用户/新工单/待处理工单/到期中服务），推送给管理员。
// 示范能力：CronContributor（定时任务）+ ConfigSchemaProvider（自动配置页）
// + Host.Notify.NotifyAdminOnce（按日去重管理员通知）。
package dailyreport

import (
	"net/http"
	"strconv"

	"lumeidc/internal/plugin"
)

// Name 插件名常量。
const Name = "dailyreport"

type Plugin struct {
	host *plugin.Host
}

func (p *Plugin) Info() plugin.Info {
	return plugin.Info{
		Name:        Name,
		Title:       "每日运营报告",
		Version:     "1.0.0",
		Description: "每天定时汇总昨日订单/收入/新用户/工单/到期服务，推送给管理员。",
	}
}

func (p *Plugin) Init(h *plugin.Host) error {
	p.host = h
	return nil
}

// ConfigSchema 配置项：开关 + 发送时刻。
func (p *Plugin) ConfigSchema() []plugin.ConfigField {
	hours := make([]plugin.ConfigOption, 0, 24)
	for h := 0; h < 24; h++ {
		s := strconv.Itoa(h)
		hours = append(hours, plugin.ConfigOption{Value: s, Label: s + " 点"})
	}
	return []plugin.ConfigField{
		{Key: "enabled", Title: "启用", Type: "switch"},
		{Key: "hour", Title: "发送时刻", Type: "select", Options: hours, Default: "8", Tip: "每日发送时间（本地时区）；修改后重启服务生效"},
	}
}

func (p *Plugin) AdminMenu() plugin.MenuItem {
	return plugin.MenuItem{Title: "每日运营报告", Icon: "ri:bar-chart-box-line", Parent: plugin.MenuGroupOps}
}

// RegisterAdminRoutes 提供「立即发送」用于联调验证（幂等：按日 key 去重）。
func (p *Plugin) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /send-now", p.sendNow)
}

func init() { plugin.Register(&Plugin{}) }
