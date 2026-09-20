package easypanel

import (
	"lumeidc/internal/server"
)

// ProductFormSpec 声明 EasyPanel 产品表单的结构化字段（替代旧 HTML+脚本插槽）。
//
// 旧实现：productform.html 引用宿主页面全局变量（cfgOptions/renderCfgList/cfgSync），
// 站点类型（虚拟主机/CDN）→ 增删隐藏配置项 cdn。声明化后由后台 SPA 渲染并联动。
func (Provider) ProductFormSpec() []server.ProductFormField {
	return []server.ProductFormField{{
		Key:   "ep_site_type",
		Type:  "select",
		Label: "站点类型",
		Options: []server.ProductFormOption{
			{Value: "vh", Label: "虚拟主机"},
			{Value: "cdn", Label: "CDN 站点"},
		},
		Hint: "CDN 站点 = 隐藏配置项 cdn=1 随订单下发（购买页不可见、不计价）；仅弹性模式（PID 留空）生效。",
		// 该字段只驱动配置项联动、不随表单提交
		Transient: true,
		ConfigByValue: map[string]*server.ProductFormConfigOption{
			"cdn": {
				Field:  "cdn",
				Name:   "站点类型",
				Mode:   "select",
				Hidden: true,
				Subs:   []server.SubOption{{Name: "CDN站点", Value: "1"}},
			},
		},
	}}
}
