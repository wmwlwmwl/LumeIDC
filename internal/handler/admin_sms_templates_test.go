package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

func TestDecodeSMSTemplateJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"有效对象", `{"name":"验证码","enabled":true}`, "application/json; charset=utf-8", 200},
		{"未知字段", `{"unexpected":true}`, "application/json", 400},
		{"错误类型", `{"enabled":"true"}`, "application/json", 400},
		{"空对象指针", `null`, "application/json", 400},
		{"数组", `[]`, "application/json", 400},
		{"多对象", `{} {}`, "application/json", 400},
		{"截断", `{"name":`, "application/json", 400},
		{"错误编码", "{\"name\":\"\xff\"}", "application/json", 400},
		{"表单不允许", `name=test`, "application/x-www-form-urlencoded", 415},
		{"超限", strings.Repeat(" ", smsTemplateRequestLimit+1), "application/json", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/sms-templates/save", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			var input service.SMSTemplate
			ok := decodeSMSTemplateJSON(w, r, &input)
			if w.Code != tc.status || ok != (tc.status == 200) {
				t.Fatalf("请求校验结果不符：状态=%d，结果=%v，响应=%s", w.Code, ok, w.Body.String())
			}
		})
	}
}

func TestSMSTemplateEndpointsRequireAdminAndCSRF(t *testing.T) {
	a := &Admin{}
	mux := http.NewServeMux()
	a.Register(mux)
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/admin/sms-templates"}, {"GET", "/admin/sms-scenes"}, {"GET", "/admin/sms-deliveries"},
		{"POST", "/admin/sms-templates/remote"}, {"POST", "/admin/sms-templates/save"}, {"POST", "/admin/sms-templates/delete"},
		{"POST", "/admin/sms-templates/preview"}, {"POST", "/admin/sms-scenes/save"},
	} {
		for _, sess := range []*middleware.Session{nil, {UserID: 1, IsAdmin: false}, {UserID: 1, IsAdmin: true, CSRF: "token"}} {
			r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
			r.Header.Set("Accept", "application/json")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			if sess != nil {
				r = r.WithContext(middleware.WithSession(r.Context(), sess))
			}
			w := httptest.NewRecorder()
			if sess != nil && sess.IsAdmin && endpoint.method == "POST" {
				middleware.CSRF(mux).ServeHTTP(w, r)
				if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "页面已过期") {
					t.Fatalf("缺失令牌未拒绝：%s，响应=%s", endpoint.path, w.Body.String())
				}
			} else {
				mux.ServeHTTP(w, r)
				want := http.StatusUnauthorized
				if sess != nil && sess.IsAdmin {
					want = http.StatusServiceUnavailable
				}
				if w.Code != want {
					t.Fatalf("鉴权或服务不可用状态不符：%s，状态=%d", endpoint.path, w.Code)
				}
			}
		}
	}
}

func TestSMSDeleteRequiresConfirmation(t *testing.T) {
	a := &Admin{Notifier: &service.Notifier{}}
	for _, body := range []string{`{"id":1}`, `{"id":0,"confirm":true}`, `{"id":-1,"confirm":true}`} {
		r := httptest.NewRequest(http.MethodPost, "/admin/sms-templates/delete", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(middleware.WithSession(r.Context(), &middleware.Session{UserID: 1, IsAdmin: true}))
		w := httptest.NewRecorder()
		a.adminSMSTemplateDelete(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("删除确认或编号校验失效：%s", w.Body.String())
		}
	}
}
