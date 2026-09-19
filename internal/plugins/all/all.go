// Package all 聚合全部业务插件：blank import 触发各自 init() 自注册到
// plugin 注册表。新增插件在此加一行 import，组合根（httpserver）无需改动。
package all

import (
	_ "lumeidc/internal/plugins/announcement"
	_ "lumeidc/internal/plugins/tickets"
	_ "lumeidc/internal/plugins/webhooknotify"
)
