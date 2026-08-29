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
		{Name: "api_url", Label: "API 地址", Placeholder: "https://upstream.example.com", Required: true},
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
