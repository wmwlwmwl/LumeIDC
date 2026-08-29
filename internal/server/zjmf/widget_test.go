package zjmf

import (
	"context"
	"strings"
	"testing"

	"lumeidc/internal/server"
)

func TestDetailWidgetRendersAllSections(t *testing.T) {
	html, err := (Provider{}).DetailWidget(context.Background(), server.Config{}, 18, server.WidgetData{
		ServiceID:  18,
		CSRF:       "csrf\"</script><script>alert(1)</script>",
		Password:   "Abc'\"<123456",
		StatusText: "激活",
		Overview: server.HostOverview{
			Detail: server.HostDetail{Username: "root", IP: "192.0.2.10", OSVersion: "Ubuntu", OSName: "Ubuntu"},
			Summary: server.ModuleSummary{
				Areas: []server.ModuleArea{
					{Key: "snapshot", Name: "快照"},
					{Key: "nat_acl", Name: "NAT"},
					{Key: "nat_web", Name: "共享建站"},
					{Key: "security_groups", Name: "安全组"},
					{Key: "setting", Name: "设置"},
				},
				Buttons:  []server.ModuleButton{{Function: "reset_network", Name: "重置网络"}},
				HasChart: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		"实例信息", "开机", "关机", "重启", "硬重启", "硬关机", "救援系统", "退出救援", "VNC 控制台",
		"登录信息", "重置密码", "随机生成", "系统信息", "重装系统",
		"监控图表", "echarts.min.js", "网络流量", "zjTrafficChart",
		"快照/备份", "创建快照", "创建备份", "restoreSnap", "restoreBackup", "delSnap", "delBackup",
		"NAT转发", "addNatAcl", "delNatAcl", "共享建站", "addNatWeb", "delNatWeb",
		"安全组", "createSecurityGroup", "linkSecurityGroup", "delSecurityGroup", "新增策略", "createSecurityRule", "delSecurityRule",
		"挂载 ISO", "mountIso", "启动顺序", "setBootOrder", "上游模块操作", "/services/18/module/reset_network",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("widget 缺少 %q", want)
		}
	}
	if strings.Contains(s, "</script><script>alert(1)</script>") {
		t.Error("CSRF 未经 JavaScript 上下文安全编码")
	}
	if strings.Contains(s, "http://") || strings.Contains(s, "https://") && !strings.Contains(s, "cdn.jsdelivr.net/npm/echarts") {
		t.Error("widget 暴露了非 ECharts CDN 的外部地址")
	}
	for _, path := range []string{"/power", "/console", "/snapshot", "/blocks", "/block-rules"} {
		if !strings.Contains(s, path) {
			t.Errorf("widget 缺少本站 API 路径 %q", path)
		}
	}
}
