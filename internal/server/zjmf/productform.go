package zjmf

import (
	"embed"
	"html/template"
	"strings"

	"lumeidc/internal/server"
)

//go:embed productform.html
var productFormFS embed.FS

var productFormTpl = template.Must(template.ParseFS(productFormFS, "productform.html"))

// ProductFormHints PID 输入提示；目录下拉/拉取按钮由 ProductFormWidget 提供。
func (Provider) ProductFormHints() server.ProductFormHints {
	return server.ProductFormHints{PIDHint: "填上游商品 ID，可从上方下拉选择后自动填入。"}
}

// ProductFormWidget ZJMF 产品表单独立区块：上游商品目录下拉 + 拉取配置项/基础价。
func (Provider) ProductFormWidget() (template.HTML, error) {
	var sb strings.Builder
	if err := productFormTpl.Execute(&sb, nil); err != nil {
		return "", err
	}
	return template.HTML(sb.String()), nil
}
