package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

func TestDecodeEmailTemplateJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"有效对象", `{"code":"auth_code","enabled":true}`, "application/json; charset=utf-8", 200},
		{"未知字段", `{"unexpected":true}`, "application/json", 400},
		{"错误类型", `{"enabled":"true"}`, "application/json", 400},
		{"空对象指针", `null`, "application/json", 400},
		{"多对象", `{} {}`, "application/json", 400},
		{"截断", `{"code":`, "application/json", 400},
		{"错误编码", "{\"code\":\"\xff\"}", "application/json", 400},
		{"表单不允许", `code=auth_code`, "application/x-www-form-urlencoded", 415},
		{"超限", strings.Repeat(" ", emailTemplateRequestLimit+1), "application/json", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/email-templates/save", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			var input emailTemplateInput
			ok := decodeEmailTemplateJSON(w, r, &input)
			if w.Code != tc.status || ok != (tc.status == 200) {
				t.Fatalf("请求校验结果不符：状态=%d，结果=%v，响应=%s", w.Code, ok, w.Body.String())
			}
		})
	}
}

func TestEmailTemplateEndpointsRequireAdminAndCSRF(t *testing.T) {
	a := &Admin{}
	mux := http.NewServeMux()
	a.Register(mux)
	for _, path := range []string{"/admin/email-templates", "/admin/email-templates/master", "/admin/email-templates/save", "/admin/email-templates/reset", "/admin/email-templates/preview", "/admin/email-templates/test"} {
		method := http.MethodPost
		if path == "/admin/email-templates" {
			method = http.MethodGet
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("未登录访问未拒绝：%s，状态=%d", path, w.Code)
		}
		if method == http.MethodPost {
			r = r.WithContext(middleware.WithSession(r.Context(), &middleware.Session{UserID: 1, IsAdmin: true, CSRF: "token"}))
			r.Header.Set("Sec-Fetch-Mode", "cors")
			w = httptest.NewRecorder()
			middleware.CSRF(mux).ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "页面已过期") {
				t.Fatalf("缺失令牌未拒绝：%s，响应=%s", path, w.Body.String())
			}
		}
	}
}

func TestEmailTemplateInvalidFieldsDoNotReachService(t *testing.T) {
	a := &Admin{Notifier: &service.Notifier{}}
	for _, body := range []string{
		`{"code":"auth_code","subject":"标题","body":"正文"}`,
		`{"code":"auth_code","enabled":true,"subject":"标题\r\n注入","body":"正文"}`,
		`{"code":"auth_code","enabled":true,"subject":"标题","body":" "}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/admin/email-templates/save", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		_, _, ok := a.readEmailTemplateDraft(w, r, false)
		if ok || w.Code != 400 {
			t.Fatalf("无效草稿未被拒绝：%s", w.Body.String())
		}
	}
}

func TestAdminTestEmailJSONAndForm(t *testing.T) {
	a := &Admin{Notifier: &service.Notifier{}}
	for _, tc := range []struct{ body, contentType string }{
		{`{"email":"test@example.com","account_index":-1}`, "application/json"},
		{`email=test%40example.com&account_index=-1`, "application/x-www-form-urlencoded"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/admin/settings/test-email", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.contentType)
		r = r.WithContext(middleware.WithSession(r.Context(), &middleware.Session{UserID: 1, IsAdmin: true}))
		w := httptest.NewRecorder()
		a.adminTestEmail(w, r)
		if !strings.Contains(w.Body.String(), "账号序号无效") {
			t.Fatalf("JSON或表单邮箱/序号未正确解析：%s", w.Body.String())
		}
	}
}

func TestEmailTestFailsClosedWithoutLimiter(t *testing.T) {
	a := &Admin{}
	r := httptest.NewRequest(http.MethodPost, "/admin/email-templates/test", nil)
	w := httptest.NewRecorder()
	if a.allowEmailTest(w, r) || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("无额度服务时不应发送邮件：%s", w.Body.String())
	}
}

func TestEmailTemplateMasterRejectsMissingState(t *testing.T) {
	a := &Admin{Notifier: &service.Notifier{}}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`} {
		r := emailTemplateAdminRequest(body, "application/json")
		w := httptest.NewRecorder()
		a.adminEmailTemplateMaster(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("缺少开关状态未被拒绝：%s", w.Body.String())
		}
	}
}

func TestEmailTemplateMasterRejectsFormBody(t *testing.T) {
	a := &Admin{Notifier: &service.Notifier{}}
	r := emailTemplateAdminRequest("enabled=1", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.adminEmailTemplateMaster(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("表单提交未被拒绝：%s", w.Body.String())
	}
}

// emailTemplateAdminRequest 构造已登录管理员的 JSON/表单请求，用于跳过鉴权只校验入参。
func emailTemplateAdminRequest(body, contentType string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/admin/email-templates/master", strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	return r.WithContext(middleware.WithSession(r.Context(), &middleware.Session{UserID: 1, IsAdmin: true}))
}
