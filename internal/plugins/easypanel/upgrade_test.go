package easypanel

// 升级调用形态测试：EasyPanel 没有独立的"换商品"接口，靠 add_vh&edit=1 改站点配置。
// 这里锁定请求参数形态，防止后人误改成 init=1（那会变成"新建站点"）或漏传配额。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lumeidc/internal/server"
)

// upgradeEnv 起一个假 EP，记录最后一次 add_vh 的查询参数。
func upgradeEnv(t *testing.T, code int) (server.Config, func() url.Values) {
	t.Helper()
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/index.php" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("a") == "add_vh" {
			last = q
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":` + string(rune('0'+code/100%10)) + string(rune('0'+code/10%10)) + string(rune('0'+code%10)) + `}`))
	}))
	t.Cleanup(srv.Close)
	return server.Config{APIURL: srv.URL, APIKey: "skey"}, func() url.Values { return last }
}

// PID 模式：换 product_id，且必须是 edit=1（不是 init=1）。
func TestUpgradeProductMode(t *testing.T) {
	cfg, last := upgradeEnv(t, 200)
	err := Provider{}.Upgrade(context.Background(), cfg, 123, server.UpgradeRequest{
		OrderID: 9, TargetPID: 5, Cycle: "monthly",
	}, nil)
	if err != nil {
		t.Fatalf("升级不应失败: %v", err)
	}
	q := last()
	if q == nil {
		t.Fatal("未发出 add_vh 请求")
	}
	if q.Get("edit") != "1" || q.Get("init") != "" {
		t.Fatalf("必须是 edit=1 且不带 init，实得 edit=%q init=%q", q.Get("edit"), q.Get("init"))
	}
	if q.Get("name") != "u123" {
		t.Fatalf("站点名应为 u123，实得 %q", q.Get("name"))
	}
	if q.Get("product_id") != "5" {
		t.Fatalf("应传 product_id=5，实得 %q", q.Get("product_id"))
	}
}

// 弹性模式：透传配额字段，并补齐必要默认值。
func TestUpgradeFlexibleMode(t *testing.T) {
	cfg, last := upgradeEnv(t, 200)
	err := Provider{}.Upgrade(context.Background(), cfg, 123, server.UpgradeRequest{
		OrderID:    9,
		Cycle:      "monthly",
		ConfigOpts: map[string]string{"web_quota": "300", "db_quota": "50", "flow_limit": "1024m"},
	}, nil)
	if err != nil {
		t.Fatalf("升级不应失败: %v", err)
	}
	q := last()
	if q.Get("web_quota") != "300" || q.Get("db_quota") != "50" || q.Get("flow_limit") != "1024m" {
		t.Fatalf("配额未透传: web_quota=%q db_quota=%q flow_limit=%q",
			q.Get("web_quota"), q.Get("db_quota"), q.Get("flow_limit"))
	}
	// 未提供的字段要有默认值，否则 EP 会把它们清空
	if q.Get("templete") != "easypanel" || q.Get("module") != "php" {
		t.Fatalf("缺少默认模板参数: templete=%q module=%q", q.Get("templete"), q.Get("module"))
	}
	if q.Get("product_id") != "" {
		t.Fatalf("弹性模式不应传 product_id，实得 %q", q.Get("product_id"))
	}
}

// 站点不存在（500）：必须显式报错并说明原因，不能让调用方以为升级成功。
func TestUpgradeSiteMissing(t *testing.T) {
	cfg, _ := upgradeEnv(t, 500)
	err := Provider{}.Upgrade(context.Background(), cfg, 123, server.UpgradeRequest{
		OrderID: 9, TargetPID: 5,
	}, nil)
	if err == nil {
		t.Fatal("站点不存在时必须报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误应说明站点不存在，实得: %v", err)
	}
}

// 主机标识非法：不能拼出 u0 这种站点名去改别人的站。
func TestUpgradeInvalidHostID(t *testing.T) {
	cfg, last := upgradeEnv(t, 200)
	if err := (Provider{}).Upgrade(context.Background(), cfg, 0, server.UpgradeRequest{}, nil); err == nil {
		t.Fatal("非法主机标识应报错")
	}
	if last() != nil {
		t.Fatal("非法入参时不应发出请求")
	}
}
