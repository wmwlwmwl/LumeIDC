package server

// CredentialField 凭据表单字段声明：provider 自带后台 UI（服务器表单按上游类型动态渲染，
// 参照 FOSSBilling 模块自带设置页的做法）。字段映射到 servers 表固定列。
type CredentialField struct {
	Name        string `json:"name"`                  // api_url / api_username / api_key
	Label       string `json:"label"`                 // 表单显示名
	Placeholder string `json:"placeholder,omitempty"` // 输入提示
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"` // password 输入框
}

// ProviderUI 可选：声明凭据表单字段。未实现时用默认三件套（地址/用户名/密钥）。
type ProviderUI interface {
	CredentialFields() []CredentialField
}

// DefaultCredentialFields 通用凭据字段（zjmf 形态）。
func DefaultCredentialFields() []CredentialField {
	return []CredentialField{
		{Name: "api_url", Label: "API 地址", Placeholder: "请输入上游 API 地址", Required: true},
		{Name: "api_username", Label: "API 用户名"},
		{Name: "api_key", Label: "API 密钥（密码）", Secret: true},
	}
}

// CredentialFieldsFor 取供应商凭据字段（未实现 ProviderUI 或声明为空时用默认）。
func CredentialFieldsFor(p Provider) []CredentialField {
	if ui, ok := p.(ProviderUI); ok {
		if f := ui.CredentialFields(); len(f) > 0 {
			return f
		}
	}
	return DefaultCredentialFields()
}

// ---- 产品表单区块：结构化声明（替代早期的 HTML+脚本插槽） ----

// ProductFormOption select 字段的一个选项。
type ProductFormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group,omitempty"` // 动态来源（OptionsURL）时用于分组
}

// ProductFormField 供应商产品表单的字段声明。
//
// 早期实现由供应商提供 HTML + 脚本（provFormRegister），脚本会直接引用宿主页面的
// DOM 与全局变量（serverSel/pidInput/cfgOptions），导致表单无法组件化。改为声明式后，
// 后台 SPA 负责渲染控件与执行以下联动：
//   - OptionsURL：从该地址拉取选项（{server_id} 会被替换）；
//   - SyncName：选中后用选项名回填产品名称；
//   - PullConfig：选中后拉取上游配置项/基础价/描述/库存并回填；
//   - ConfigByValue：字段值 → 增删某个隐藏配置项（如 EasyPanel 的 cdn）。
type ProductFormField struct {
	Key      string              `json:"key"`   // 绑定的表单字段名（如 upstream_pid）
	Label    string              `json:"label"`
	Type     string              `json:"type"`  // select | text
	Hint     string              `json:"hint,omitempty"`
	Options  []ProductFormOption `json:"options,omitempty"`
	OptionsURL string            `json:"options_url,omitempty"`
	SyncName   bool              `json:"sync_name,omitempty"`
	PullConfig bool              `json:"pull_config,omitempty"`
	// Transient 为真表示该字段仅驱动前端联动、不随表单提交（如站点类型）。
	Transient bool `json:"transient,omitempty"`
	// ConfigByValue：值 → 需要存在的隐藏配置项（空值/未命中表示不存在）。
	ConfigByValue map[string]*ProductFormConfigOption `json:"config_by_value,omitempty"`
}

// ProductFormConfigOption 由产品表单字段联动写入的隐藏配置项。
type ProductFormConfigOption struct {
	Field  string   `json:"field"`
	Name   string   `json:"name"`
	Mode   string   `json:"option_mode"`
	Hidden bool     `json:"hidden"`
	Subs   []SubOption `json:"sub,omitempty"`
}

// SubOption 配置项子项（value 固定为字符串，便于跨包传输）。
type SubOption struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ProductFormSpecProvider 可选：声明产品表单区块（结构化）。
type ProductFormSpecProvider interface {
	ProductFormSpec() []ProductFormField
}

// ProductFormSpecFor 取供应商的产品表单声明（未实现时为空 = 无专属区块）。
func ProductFormSpecFor(p Provider) []ProductFormField {
	if sp, ok := p.(ProductFormSpecProvider); ok {
		return sp.ProductFormSpec()
	}
	return nil
}
