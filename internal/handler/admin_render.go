package handler

import (
	"embed"
	"html/template"

	"lumeidc/internal/server"
	"lumeidc/internal/update"
)

const (
	mailAccountsKey = "smtp_accounts"
	mailCooldownKey = "smtp_cooldown_seconds"
)

//go:embed templates/admin.html templates/admin_*.html
var adminFS embed.FS

type AdminData struct {
	Page          string
	Rows          any
	Error         string
	Msg           string
	CSRF          string
	Product       any
	Types         any
	Monthly       string
	Quarterly     string
	Yearly        string
	ConfigJSON    string
	ServersList   any
	UpstreamBound bool
	Secret        string
	TotalProfit   string
	// 站点品牌（由 renderAdmin 统一注入）
	SiteName  string
	SiteMark  string
	Providers []server.ProviderInfo // 上游供应商清单（服务器表单下拉）
	// ProviderFieldsJSON 服务器表单动态凭据字段：{"fields":{code:[...]}, "values":{api_url:...}}。
	ProviderFieldsJSON template.JS
	// ProductHintsJSON 产品表单供应商差异声明：{code:{markupFree,hideCatalog,pidHint}}。
	ProductHintsJSON template.JS
	// ProviderWidgets 供应商产品表单独立区块（插槽，按当前供应商显隐）。
	ProviderWidgets template.HTML
	// GlobalProfit 全局默认利润（产品未单独设置时回退）
	GlobalProfitType  int64
	GlobalProfitValue float64
	// ServerProfit 服务器默认利润（导入表单预填）
	ServerProfitType  int16
	ServerProfitValue float64
	// UpdateInfo 系统更新页数据（nil 时不渲染）；UpdateDisabled 非 Linux 平台禁用。
	UpdateInfo     *update.Info
	UpdateDisabled bool
	// UpdatePending 已有新版本二进制待重启生效（页面恢复「立即重启」入口，避免重复下载）。
	UpdatePending bool
}

type adminRow struct {
	ID         int64
	A, B, C, D string // 通用多列展示，避免为每张表写模板
	E          string // 扩展列（如订单利润）
	F          string // 支付实付/手续费等审计信息
}
