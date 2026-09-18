package easypanel

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumeidc/internal/server"
)

func TestCatalogParsesMigrationProductList(t *testing.T) {
	payload := `[{
		"id": 7,
		"product_name": "PHP 共享主机",
		"web_quota": 2048,
		"db_quota": 512,
		"domain": -1,
		"module": "php",
		"templete": "easypanel",
		"ftp": 1
	}]`
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/index.php" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("a") != "migrate_list_product" {
			t.Fatalf("action=%q, want migrate_list_product", r.URL.Query().Get("a"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":200,"products":"` + encoded + `"}`))
	}))
	defer srv.Close()

	list, err := (Provider{}).Catalog(context.Background(), server.Config{APIURL: srv.URL, APIKey: "skey"})
	if err != nil {
		t.Fatalf("Catalog() error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Catalog() len=%d, want 1", len(list))
	}
	if list[0].PID != 7 || list[0].Name != "PHP 共享主机" {
		t.Fatalf("product=%+v, want pid=7/name=PHP 共享主机", list[0])
	}
	for _, want := range []string{"网页空间 2048M", "数据库 512M", "域名不限"} {
		if !strings.Contains(list[0].Description, want) {
			t.Fatalf("description=%q missing %q", list[0].Description, want)
		}
	}
	for _, unwanted := range []string{"模块", "模板", "FTP"} {
		if strings.Contains(list[0].Description, unwanted) {
			t.Fatalf("description=%q should not contain %q", list[0].Description, unwanted)
		}
	}
	if got, want := list[0].Description, "网页空间 2048M<br>数据库 512M<br>域名不限"; got != want {
		t.Fatalf("description=%q want %q", got, want)
	}
	if list[0].Stock != -1 {
		t.Fatalf("stock=%d, want -1", list[0].Stock)
	}
}

func TestSiteName(t *testing.T) {
	if got := SiteName(25); got != "u25" {
		t.Fatalf("SiteName(25)=%q want u25", got)
	}
	if got := SiteName(1); got != "u1" {
		t.Fatalf("SiteName(1)=%q", got)
	}
}

func TestServiceIDFromHost(t *testing.T) {
	if id, err := serviceIDFromHost(25); err != nil || id != 25 {
		t.Fatalf("serviceIDFromHost(25)=%d,%v", id, err)
	}
	for _, bad := range []int64{0, -1} {
		if _, err := serviceIDFromHost(bad); err == nil {
			t.Fatalf("serviceIDFromHost(%d) 应报错", bad)
		}
	}
}

// 签名算法验证：s = md5(a + skey + r)。
// 顺序已用真实面板实测（a=info + skey + r 返回 200）；
// 文档示例 URL 中的 s 值对应另一把 skey，其内联向量有误，此处用标准 md5 值。
func TestSignatureVector(t *testing.T) {
	got := md5hex("add", "test", "888")
	want := "d0af29175f7870817aa0168a20bdccd1" // = md5("addtest888")
	if got != want {
		t.Fatalf("签名不符: got %s want %s", got, want)
	}
	if got := md5hex("info", "k", "1"); got != md5hex("info", "k", "1") {
		t.Fatal("签名应确定性")
	}
}

func TestStrField(t *testing.T) {
	m := map[string]any{
		"s":     "abc",
		"n":     float64(42),
		"f":     float64(3.5),
		"b":     true,
		"nil":   nil,
		"numst": "123",
	}
	cases := map[string]string{"s": "abc", "n": "42", "f": "3.5", "b": "1", "nil": "", "numst": "123"}
	for k, want := range cases {
		if got := strField(m, k); got != want {
			t.Errorf("strField(%s)=%q want %q", k, got, want)
		}
	}
	if numField(m, "n") != 42 || numField(m, "numst") != 123 || numField(m, "nil") != 0 {
		t.Errorf("numField 异常")
	}
}

func TestAPICode(t *testing.T) {
	if apiCode(nil) != 0 {
		t.Fatal("nil 错误码应为 0")
	}
	if apiCode(&errAPI{code: 500}) != 500 {
		t.Fatal("errAPI 码提取失败")
	}
	if apiCode(errString("x")) != 0 {
		t.Fatal("普通错误码应为 0")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestStatusMissingReturnsErrHostMissing 锁定 getVh 返回 500（站点不存在或面板内部错误）时
// Status 必须返回 ErrHostMissing 包装的错误，让 SyncUpstreamStatus 走累计缺失路径，
// 而不是一次就当作 terminated 直接删除。之前 Status 错误地返回 {Status: "terminated"}, nil。
func TestStatusMissingReturnsErrHostMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":500,"msg":"站点不存在"}`))
	}))
	defer srv.Close()

	cfg := server.Config{APIURL: srv.URL, APIKey: "skey"}
	_, err := Provider{}.Status(context.Background(), cfg, 999)
	if err == nil {
		t.Fatal("Status 返回 nil error，应该返回 ErrHostMissing")
	}
	if !server.IsHostMissing(err) {
		t.Fatalf("错误未被 IsHostMissing 识别: %v", err)
	}
}
