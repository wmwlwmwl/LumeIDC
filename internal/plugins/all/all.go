// Package all 聚合全部自注册扩展：业务插件（plugin.Register）与
// 内置上游供应商（server.Register 接口类，非业务插件，不出现在插件管理页）。
// 新增扩展在此加一行 blank import，组合根（httpserver）无需改动。
package all

import (
	_ "lumeidc/internal/plugins/announcement"
	_ "lumeidc/internal/plugins/dailyreport"
	_ "lumeidc/internal/plugins/easypanel" // 供应商（接口类）
	_ "lumeidc/internal/plugins/tickets"
	_ "lumeidc/internal/plugins/webhooknotify"
	_ "lumeidc/internal/plugins/zjmf" // 供应商（接口类）
)
