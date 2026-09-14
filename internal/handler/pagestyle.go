package handler

import (
	_ "embed"
	"html/template"
)

//go:embed templates/artpage.css
var artpageCSS string

// artStyleTag 独立 SSR 页（扫码支付/模拟支付/安装结果）共享的 <style> 片段，
// 样式对齐 Art Design Pro 令牌，见 templates/artpage.css 文件头注释。
var artStyleTag = "<style>" + artpageCSS + "</style>"

// artPageFuncs 模板经 {{artStyle}} 注入共享样式；CSS 为本地嵌入内容，输出安全。
var artPageFuncs = template.FuncMap{
	"artStyle": func() template.HTML { return template.HTML(artStyleTag) },
}

// parseArtTemplate 用 artPageFuncs 解析嵌入模板；模板内通过 {{artStyle}} 引入样式。
func parseArtTemplate(pattern string) (*template.Template, error) {
	return template.New("").Funcs(artPageFuncs).ParseFS(tplFS, pattern)
}
