package zjmf

import (
	"lumeidc/internal/server"
)

// ProductFormSpec 声明 ZJMF 产品表单的结构化字段（替代旧 HTML+脚本插槽）。
//
// 旧实现：productform.html 引用宿主页面的 DOM 元素（serverSel/pidInput/cfgOptions）
// 与全局函数（renderCfgList/cfgSync），导致表单无法组件化。
// 声明化后，后台 SPA 负责渲染控件与执行 SyncName/PullConfig 联动。
func (Provider) ProductFormSpec() []server.ProductFormField {
	return []server.ProductFormField{{
		Key:         "upstream_pid",
		Type:        "select",
		Label:       "上游商品（按分组）",
		OptionsURL:  "/admin/products/upstream-options?server_id={server_id}",
		SyncName:    true,
		PullConfig:  true,
		Hint:        "选择上游商品后，名称/价格/库存/配置项会自动带出（仍可手动修改）。保存后支付成功将自动向上游开通。",
	}}
}

// 删除旧实现：ProductFormWidget、productform.html、embed
