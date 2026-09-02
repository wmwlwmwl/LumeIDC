package handler

import (
	"html/template"
	"testing"
)

// TestAdminTemplatesParse 兜底校验后台模板可解析（避免 go:embed 模板语法错误漏检）。
func TestAdminTemplatesParse(t *testing.T) {
	for _, page := range []string{"admin_users.html", "admin_user_form.html"} {
		if _, err := template.ParseFS(adminFS, "templates/admin.html", "templates/"+page); err != nil {
			t.Fatalf("%s parse failed: %v", page, err)
		}
	}
}

// TestSiteTemplatesParse 兜底校验前台（site 布局）模板可解析。
func TestSiteTemplatesParse(t *testing.T) {
	for _, page := range []string{"service_detail.html", "service_upgrade.html"} {
		if _, err := template.New(page).Funcs(template.FuncMap{"safeDescriptionHTML": safeDescriptionHTML}).
			ParseFS(siteFS, "templates/site.html", "templates/"+page); err != nil {
			t.Fatalf("%s parse failed: %v", page, err)
		}
	}
}
