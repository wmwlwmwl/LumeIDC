package handler

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"lumeidc/internal/repo"
)

// 自检：购买页隐藏配置项（如 EP cdn=1）应输出固定 hidden input 而非客户可选控件，
// 非隐藏项照常渲染 select。与 CalculateQuote 跳过 hidden 计价的口径配套。
func TestBuyHiddenOptionRender(t *testing.T) {
	tpl, err := template.ParseFS(siteFS, "templates/site.html", "templates/buy.html")
	if err != nil {
		t.Fatalf("模板解析失败: %v", err)
	}
	data := map[string]any{
		"Product": &repo.Product{ID: 1, Name: "p"},
		"Monthly": "1.00", "Quarterly": "3.00", "Yearly": "12.00",
		"ShowQuarterly": true, "ShowYearly": true,
		"CSRF": "t", "LoggedIn": true,
		"Options": []repo.ConfigOption{
			{Field: "cdn", Name: "站点类型", Mode: "select", Hidden: true,
				Subs: []repo.ConfigValue{{Name: "CDN站点", Value: "1"}}},
			{Field: "os", Name: "系统", Mode: "select",
				Subs: []repo.ConfigValue{{Name: "Ubuntu"}}},
		},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "site", data); err != nil {
		t.Fatalf("模板执行失败: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `name="cfg_cdn"`) || !strings.Contains(out, `value="1"`) {
		t.Errorf("隐藏项应输出 hidden input cdn=1:\n%s", out)
	}
	if strings.Contains(out, ">CDN站点</option>") {
		t.Errorf("隐藏项不应渲染为可选 option:\n%s", out)
	}
	if !strings.Contains(out, `name="cfg_os"`) || !strings.Contains(out, ">Ubuntu</option>") {
		t.Errorf("非隐藏项应照常渲染 select:\n%s", out)
	}
}
