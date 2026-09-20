package zjmf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lumeidc/internal/server"
)

func collect(t *testing.T, body string) []server.UpstreamProduct {
	t.Helper()
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	var out []server.UpstreamProduct
	walkCatalog(raw.Data, "", &out)
	return out
}

// 真实 ZJMF 响应：data.products 分组嵌套 products 商品数组
func TestWalkCatalogNested(t *testing.T) {
	out := collect(t, `{"status":200,"data":{"products":[{"id":55,"name":"国内服务器","products":[
		{"id":923,"name":"国内A"},
		{"id":924,"name":"国内B"}
	]}]}}`)
	if len(out) != 2 {
		t.Fatalf("期望 2 个商品，得到 %d", len(out))
	}
	if out[0].PID != 923 || out[0].Name != "国内A" || out[0].GroupName != "国内服务器" {
		t.Fatalf("第一条错误: %+v", out[0])
	}
}

// mock 形状：data 数组、组内 "product" 键
func TestWalkCatalogFlat(t *testing.T) {
	out := collect(t, `{"status":200,"data":[{"id":1,"name":"云服务器","product":[{"id":100,"name":"美国精品"}]}]}`)
	if len(out) != 1 || out[0].PID != 100 || out[0].GroupName != "云服务器" {
		t.Fatalf("flat 解析错误: %+v", out)
	}
}

func TestCatalogConfigRequestsAreSingleConcurrent(t *testing.T) {
	var mu sync.Mutex
	current, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/cart/all":
			w.Write([]byte(`{"status":200,"data":[{"id":1,"name":"云服务器","product":[{"id":100,"name":"产品A"},{"id":101,"name":"产品B"},{"id":102,"name":"产品C"}]}]}`))
		case "/cart/get_product_config":
			mu.Lock()
			current++
			if current > peak {
				peak = current
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			current--
			mu.Unlock()
			w.Write([]byte(`{"status":200,"data":{"product":{"price":10}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	products, err := (Provider{}).Catalog(context.Background(), server.Config{APIURL: srv.URL, APIUsername: "api", APIKey: "secret", CredentialRevision: 1})
	if err != nil {
		t.Fatalf("Catalog 返回错误: %v", err)
	}
	if len(products) != 3 {
		t.Fatalf("期望 3 个商品，得到 %d", len(products))
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 1 {
		t.Fatalf("get_product_config 最大并发为 %d，期望不超过 1", peak)
	}
}
