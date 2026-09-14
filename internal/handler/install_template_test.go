package handler

import (
	"testing"
)

// TestInstallTemplatesParse 校验仍由 SSR 承载的安装结果模板（done/error）可解析，
// 且经 artPageFuncs 提供 {{artStyle}}（与实际渲染路径 parseArtTemplate 一致）。
// 其余前台/后台页面均已 SPA 化，不再有模板。
func TestInstallTemplatesParse(t *testing.T) {
	for _, name := range []string{"done.html", "error.html"} {
		if _, err := parseArtTemplate("templates/" + name); err != nil {
			t.Errorf("%s parse failed: %v", name, err)
		}
	}
}
