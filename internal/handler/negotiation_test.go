package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

func TestWantsJSON(t *testing.T) {
	cases := []struct {
		accept string
		want   bool
	}{
		{"application/json", true},                  // SPA fetch（http 客户端显式携带）
		{"application/json, text/plain, */*", true}, // fetch 默认 + json 显式
		{"text/html", false},                        // 浏览器导航 → SSR
		{"text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8", false}, // 浏览器含 json 但优先 html → SSR
		{"*/*", false}, // 裸 fetch 默认：不给 SSR
		{"", false},    // 空
		{"application/json; charset=utf-8", true}, // 带 charset
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", c.accept)
		if got := wantsJSON(r); got != c.want {
			t.Errorf("wantsJSON(%q) = %v, want %v", c.accept, got, c.want)
		}
	}
}

func TestBodyValuesJSON(t *testing.T) {
	body := `{"product_id":20,"cycle":"yearly","cfg_cpu":"2","cfg_mem":4,"enabled":true,"coupon":""}`
	r := httptest.NewRequest("POST", "/order", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	vals, err := bodyValues(r)
	if err != nil {
		t.Fatalf("bodyValues: %v", err)
	}
	want := map[string]string{"product_id": "20", "cycle": "yearly", "cfg_cpu": "2", "cfg_mem": "4", "enabled": "true", "coupon": ""}
	for k, v := range want {
		if vals[k] != v {
			t.Errorf("bodyValues[%q] = %q, want %q", k, vals[k], v)
		}
	}
}

func TestBodyValuesForm(t *testing.T) {
	r := httptest.NewRequest("POST", "/order", bytes.NewBufferString("product_id=20&cycle=monthly&cfg_cpu=2"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	vals, err := bodyValues(r)
	if err != nil {
		t.Fatalf("bodyValues(form): %v", err)
	}
	if vals["product_id"] != "20" || vals["cycle"] != "monthly" || vals["cfg_cpu"] != "2" {
		t.Fatalf("form bodyValues = %v", vals)
	}
}

func TestProductListJSON(t *testing.T) {
	views := []productView{{ID: 1, Name: "n", Desc: "d", Monthly: "3.10", Stock: -1}}
	out := productListJSON(views)
	if len(out) != 1 || out[0]["id"].(int64) != 1 || out[0]["name"] != "n" || out[0]["monthly"] != "3.10" {
		t.Fatalf("productListJSON = %v", out)
	}
}

func TestTypeNavJSON(t *testing.T) {
	nav := []typeNav{{
		ID: 47, Name: "一级",
		Children: []repo.ProductType{{ID: 48, Name: "二级", Description: "desc"}},
	}}
	out := typeNavJSON(nav)
	if len(out) != 1 {
		t.Fatalf("typeNavJSON len = %d", len(out))
	}
	if out[0]["children"].([]map[string]any)[0]["name"] != "二级" {
		t.Fatalf("typeNavJSON children = %v", out[0]["children"])
	}
}

func TestAnnouncementJSON(t *testing.T) {
	list := []map[string]any{
		{"ID": int64(1), "Title": "t", "Content": "c", "Pinned": true, "CreatedAt": "2026-08-28"},
	}
	out := announcementJSON(list)
	if len(out) != 1 || out[0]["title"] != "t" || out[0]["pinned"] != true {
		t.Fatalf("announcementJSON = %v", out)
	}
}

// TestJSONStatusSSR 浏览器（无 Accept:json）→ 原生 http.Error（HTML）。
func TestJSONStatusSSR(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil) // 无 Accept
	jsonStatus(rec, r, 400, "参数错误")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "参数错误") {
		t.Fatalf("SSR status: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("SSR content-type = %q, want text/plain", ct)
	}
}

// TestJSONStatusJSON SPA（Accept:json）→ 带原状态码的 JSON，ok=0。
func TestJSONStatusJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("Accept", "application/json")
	jsonStatus(rec, r, 202, "验证码已发送")
	if rec.Code != 202 {
		t.Fatalf("code = %d, want 202", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"msg":"验证码已发送"`) {
		t.Fatalf("JSON status body = %q", body)
	}
}

func TestJSONErrorStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("Accept", "application/json")
	jsonStatus(rec, r, 429, "尝试次数过多")
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("error status: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestCreateOrderContentNegotiation 验证 createOrder 对 JSON 与 SSR 的分野
// 只到鉴权层：未登录时 SPA 收到 401 JSON（RequireUser），浏览器收到跳转。
func TestCreateOrderAuthJSON(t *testing.T) {
	// SPA 未登录 → 401 JSON（非导航请求；浏览器 fetch 自动带 Sec-Fetch-Mode: cors）
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/order", bytes.NewBufferString(`{"product_id":1}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r = r.WithContext(middleware.WithSession(r.Context(), nil))
	middleware.RequireUser(rec, r)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"ok":0`) {
		t.Fatalf("SPA unauthorized: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// 浏览器（导航）未登录 → 303 跳登录
	rec2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/order", bytes.NewBufferString("product_id=1"))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.Header.Set("Sec-Fetch-Mode", "navigate")
	r2 = r2.WithContext(middleware.WithSession(r2.Context(), nil))
	middleware.RequireUser(rec2, r2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("SSR unauthorized: code=%d", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("SSR unauthorized location = %q", loc)
	}
}
