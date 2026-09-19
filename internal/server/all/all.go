// Package all 聚合全部内置供应商：blank import 触发各自 init() 自注册到
// server.DefaultRegistry。新增供应商在此加一行 import，组合根无需改动。
package all

import (
	_ "lumeidc/internal/server/easypanel"
	_ "lumeidc/internal/server/zjmf"
)
