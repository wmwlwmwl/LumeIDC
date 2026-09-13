package handler

import (
	"html/template"

	"lumeidc/internal/update"
)

const (
	mailAccountsKey = "smtp_accounts"
	mailCooldownKey = "smtp_cooldown_seconds"
)

// AdminData 后台表单/页面的内部数据载体（不再用于模板渲染，仅组装 JSON 响应）。
type AdminData struct {
	Product       any
	Types         any
	Monthly       string
	Quarterly     string
	Yearly        string
	ConfigJSON    string
	ServersList   any
	UpstreamBound bool
	// ProductHintsJSON 产品表单供应商差异声明：{code:{markupFree,hideCatalog,pidHint}}。
	ProductHintsJSON template.JS
	// UpdateInfo 系统更新页数据（nil 时不返回）；UpdateDisabled 非 Linux 平台禁用。
	UpdateInfo     *update.Info
	UpdateDisabled bool
	// UpdatePending 已有新版本二进制待重启生效。
	UpdatePending bool
}
