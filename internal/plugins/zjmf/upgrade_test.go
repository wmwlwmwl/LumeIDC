package zjmf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"lumeidc/internal/server"
)

// newUpgEnv 起一个模拟魔方财务 home API 的测试服务；登录统一返回假 JWT。
func newUpgEnv(t *testing.T, h http.HandlerFunc) server.Config {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return server.Config{APIURL: srv.URL, APIUsername: "api", APIKey: "secret", CredentialRevision: 1}
}

func loginOK(out http.ResponseWriter) { out.Write([]byte(`{"jwt":"abc.def.ghi","status":200}`)) }

// headerOK 返回 host_data 带升降级开关的 /host/header 响应。
func headerOK(w http.ResponseWriter, allowUpgrade any) {
	body := `{"status":200,"data":{"host_data":{"allow_upgrade_product":` + jsonEncode(allowUpgrade) + `,"domain":"u1"}}}`
	w.Write([]byte(body))
}

// jsonEncode 把测试值序列化为 JSON 字面量（数字/布尔/字符串）。
func jsonEncode(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// 上游配置了可升级目标：host_data 开关开启 + home 形态 data.host[] 映射为 UpgradeTarget。
func TestUpgradeTargetsHomeShape(t *testing.T) {
	var targetCalled bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/header":
			headerOK(w, 1)
		case "/upgrade/upgrade_product/123":
			targetCalled = true
			w.Write([]byte(`{"status":200,"data":{"host":[
				{"pid":301,"name":"美国精品"},
				{"pid":302,"name":"香港精品"}]}}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	targets, err := p.UpgradeTargets(context.Background(), cfg, 123)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !targetCalled {
		t.Fatal("开关开启后应调用升级目标接口")
	}
	if len(targets) != 2 || targets[0].UpstreamPID != 301 || targets[0].Name != "美国精品" {
		t.Fatalf("目标解析错误: %+v", targets)
	}
}

// 开关关闭（allow_upgrade_product=0）：直接判定不可升级，不再调升级目标接口。
func TestUpgradeTargetsSwitchOff(t *testing.T) {
	var targetCalled bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/header":
			headerOK(w, 0)
		case "/upgrade/upgrade_product/6":
			targetCalled = true
			w.Write([]byte(`{"status":200,"data":{"host":[]}}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	targets, err := p.UpgradeTargets(context.Background(), cfg, 6)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if targetCalled {
		t.Fatal("开关关闭时不应调升级目标接口")
	}
	if len(targets) != 0 {
		t.Fatalf("期望无目标，得到 %+v", targets)
	}
}

// 上游不可升级（开关开启但目标接口返回 status=400 "无法升级或降级"）：返回空且不报错。
func TestUpgradeTargetsNotSupported(t *testing.T) {
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/header":
			headerOK(w, 1)
		case "/upgrade/upgrade_product/7":
			w.Write([]byte(`{"status":400,"msg":"当前产品无法升级或降级"}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	targets, err := p.UpgradeTargets(context.Background(), cfg, 7)
	if err != nil {
		t.Fatalf("不可升级不应报错: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("期望无目标，得到 %+v", targets)
	}
}

// 上游有升级能力但未配置目标（data.host 空）：返回空。
func TestUpgradeTargetsEmptyList(t *testing.T) {
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/header":
			headerOK(w, 1)
		case "/upgrade/upgrade_product/9":
			w.Write([]byte(`{"status":200,"data":{"host":[]}}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	targets, err := p.UpgradeTargets(context.Background(), cfg, 9)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("期望无目标，得到 %+v", targets)
	}
}

// 开关解析纯函数：缺失/不可解析放行；0/"0"/false 拦截；1/"1"/true 放行。
func TestAllowUpgradeProduct(t *testing.T) {
	hd := func(raw any) map[string]json.RawMessage {
		return map[string]json.RawMessage{"host_data": json.RawMessage(`{"allow_upgrade_product":` + jsonEncode(raw) + `}`)}
	}
	cases := []struct {
		name string
		data map[string]json.RawMessage
		want bool
	}{
		{"开关为数字1", hd(1), true},
		{"开关为数字0", hd(0), false},
		{"开关为字符串1", hd("1"), true},
		{"开关为字符串0", hd("0"), false},
		{"开关为false", hd(false), false},
		{"缺少host_data", map[string]json.RawMessage{}, true},
		{"缺少开关字段", map[string]json.RawMessage{"host_data": json.RawMessage(`{"domain":"u1"}`)}, true},
		{"null放行", map[string]json.RawMessage{"host_data": json.RawMessage(`{"allow_upgrade_product":null}`)}, true},
	}
	for _, c := range cases {
		if got := allowUpgradeProduct(c.data); got != c.want {
			t.Fatalf("%s: 期望 %v，得到 %v", c.name, c.want, got)
		}
	}
}

// 业务 405（token 失效）重登自愈：首次业务请求 405 → 清缓存重登一次 → 重放成功。
func TestRetryOnBiz405(t *testing.T) {
	var loginN, cartN int
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginN++
			loginOK(w)
		case "/cart/all":
			cartN++
			if cartN == 1 {
				w.Write([]byte(`{"status":405,"msg":"登录失效,请重新登录"}`))
				return
			}
			w.Write([]byte(`{"status":200,"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	})
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(context.Background(), cfg, "/cart/all", &out); err != nil {
		t.Fatalf("405 重登后应成功: %v", err)
	}
	if loginN != 2 {
		t.Fatalf("期望重登一次（共 2 次登录），实际 %d", loginN)
	}
	if cartN != 2 {
		t.Fatalf("期望请求重放一次（共 2 次），实际 %d", cartN)
	}
}

// 降级：checkout 返回 1001（上游自动退差并立即生效），不调 apply_credit。
func TestUpgradeDowngradeAutoApplied(t *testing.T) {
	var applied bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200,"msg":"成功"}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":1001,"msg":"购买成功","data":{"orderid":88}}`))
		case "/apply_credit":
			applied = true
			w.Write([]byte(`{"status":200}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{TargetPID: 402, Cycle: "monthly", DiffAmount: -30}, nil)
	if err != nil {
		t.Fatalf("降级应成功: %v", err)
	}
	if applied {
		t.Fatal("降级自动生效，不应调 apply_credit")
	}
}

// 上游未给账单号（显式 invoiceid=null，0 元升级/降级直接生效）：按已完成处理，
// 不能拿 "null" 去查金额或付款——上游只会回"未找到支付项目"。
func TestUpgradeNullInvoiceTreatedAsApplied(t *testing.T) {
	var detailCalled, payCalled bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200,"msg":"成功"}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":200,"msg":"成功","data":{"invoiceid":null}}`))
		case "/get_invoices_detail":
			detailCalled = true
			w.Write([]byte(invoiceResp(`"50.00"`)))
		case "/apply_credit":
			payCalled = true
			w.Write([]byte(`{"status":200}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	if err := p.Upgrade(context.Background(), cfg, 5,
		server.UpgradeRequest{OrderID: 82, TargetPID: 402, Cycle: "monthly", DiffAmount: 50}, newMemCheckpoint()); err != nil {
		t.Fatalf("上游未生成账单应视为已生效: %v", err)
	}
	if detailCalled || payCalled {
		t.Fatalf("没有账单就不该比价/付款，实得 detail=%v pay=%v", detailCalled, payCalled)
	}
}

// 升级：billingcycle 映射（yearly→annually）、金额核对一致后 apply_credit 支付。
func TestUpgradePaidFlow(t *testing.T) {
	var postBodies []url.Values
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		body := url.Values{}
		_ = r.ParseForm()
		for k, vs := range r.PostForm {
			for _, v := range vs {
				body.Add(k, v)
			}
		}
		if r.Method == http.MethodPost && r.URL.Path != "/zjmf_api_login" {
			postBodies = append(postBodies, body)
		}
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200,"msg":"成功"}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9001","orderid":99}}`))
		case "/get_invoices_detail":
			w.Write([]byte(`{"status":200,"data":{"detail":{"total":"50.00"}}}`))
		case "/apply_credit":
			w.Write([]byte(`{"status":200,"msg":"成功"}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{OrderID: 77, TargetPID: 402, Cycle: "yearly", DiffAmount: 50}, ck)
	if err != nil {
		t.Fatalf("升级应成功: %v", err)
	}
	if len(postBodies) != 3 {
		t.Fatalf("期望 3 次 POST（选择/结算/支付），实际 %d 次: %+v", len(postBodies), postBodies)
	}
	sel := postBodies[0]
	if sel.Get("hid") != "5" || sel.Get("pid") != "402" || sel.Get("billingcycle") != "annually" {
		t.Fatalf("升级选择参数错误: %+v", sel)
	}
	pay := postBodies[2]
	if pay.Get("invoiceid") != "9001" || pay.Get("use_credit") != "1" {
		t.Fatalf("支付参数错误: %+v", pay)
	}
	// 成功后必须清掉账单检查点，否则同一订单重试会误复用这张已付账单。
	if v, ok, _ := ck.GetCheckpoint(ckUpgradeInvoice(77)); ok {
		t.Fatalf("升级成功后应清除账单检查点，实得 %q", v)
	}
}

// 金额超差：返回 PriceChangedError，停在未付款等管理员二选一（按新价开通 / 退款）；
// 账单已记入检查点，重试才能复用同一张账单强制开通。
func TestUpgradeAmountMismatch(t *testing.T) {
	var applied bool
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":200,"msg":"结算成功","data":{"invoiceid":"9002","orderid":100}}`))
		case "/get_invoices_detail":
			w.Write([]byte(`{"status":200,"data":{"detail":{"total":"80.00"}}}`))
		case "/apply_credit":
			applied = true
			w.Write([]byte(`{"status":200}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{OrderID: 78, TargetPID: 402, Cycle: "monthly", DiffAmount: 50}, ck)
	if err == nil {
		t.Fatal("金额超差应报错")
	}
	var pce *server.PriceChangedError
	if !errors.As(err, &pce) {
		t.Fatalf("应为 PriceChangedError，实际: %v", err)
	}
	if !server.IsManualReview(err) {
		t.Fatalf("涨价应转人工复核，实际: %v", err)
	}
	if pce.UpstreamAmount != 80 || pce.ExpectAmount != 50 || pce.UpstreamInvoiceID != "9002" {
		t.Fatalf("错误内容不符: %+v", pce)
	}
	if applied {
		t.Fatal("金额不一致时不应自动支付")
	}
	if got, ok, _ := ck.GetCheckpoint(ckUpgradeInvoice(78)); !ok || got != "9002" {
		t.Fatalf("超差时账单应留在检查点供重试复用，实得 %q,%v", got, ok)
	}
}

// 管理员确认后重试：检查点里已有账单 → 不重新结算、不重新比价，直接付款即"按新价强制开通"。
func TestUpgradeReusesInvoiceFromCheckpoint(t *testing.T) {
	var selectCalls, checkoutCalls, detailCalls, payCalls int
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			selectCalls++
			w.Write([]byte(`{"status":200}`))
		case "/upgrade/checkout_upgrade_product":
			checkoutCalls++
			w.Write([]byte(`{"status":200,"data":{"invoiceid":"9100"}}`))
		case "/get_invoices_detail":
			detailCalls++
			w.Write([]byte(invoiceResp(`"80.00"`)))
		case "/apply_credit":
			payCalls++
			w.Write([]byte(`{"status":200}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	_ = ck.SetCheckpoint(ckUpgradeInvoice(79), "9100")
	// 本地差价 50、上游账单 80：若重新比价会再次超差，复用检查点则跳过比价直接付款。
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{OrderID: 79, TargetPID: 402, Cycle: "monthly", DiffAmount: 50}, ck)
	if err != nil {
		t.Fatalf("复用检查点应直接支付成功: %v", err)
	}
	if selectCalls != 0 || checkoutCalls != 0 || detailCalls != 0 {
		t.Fatalf("不应重复结算/比价，实得 select=%d checkout=%d detail=%d", selectCalls, checkoutCalls, detailCalls)
	}
	if payCalls != 1 {
		t.Fatalf("应付款一次，实得 %d", payCalls)
	}
}

// 付款失败但账单仍在上游（余额不足）：保持重试不消耗次数，检查点保留供下次重付同一张账单。
func TestUpgradeKeepsRetryingWhileInvoiceAlive(t *testing.T) {
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":200,"data":{"invoiceid":"9101"}}`))
		case "/get_invoices_detail":
			w.Write([]byte(invoiceResp(`"50.00"`)))
		case "/apply_credit":
			w.Write([]byte(`{"status":400,"msg":"余额不足"}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{OrderID: 80, TargetPID: 402, Cycle: "monthly", DiffAmount: 50}, ck)
	if err == nil {
		t.Fatal("付款失败应报错")
	}
	if !server.IsRetryLater(err) {
		t.Fatalf("余额不足应保持重试，实际: %v", err)
	}
	if server.IsManualReview(err) {
		t.Fatalf("余额不足不应转人工: %v", err)
	}
	if got, ok, _ := ck.GetCheckpoint(ckUpgradeInvoice(80)); !ok || got != "9101" {
		t.Fatalf("重试要复用同一张账单，检查点不应被清，实得 %q,%v", got, ok)
	}
}

// 付款失败且账单已被删/作废：重试永远付不掉，清检查点并转人工重新结算。
func TestUpgradeDropsCheckpointWhenInvoiceGone(t *testing.T) {
	// 首次查询（比价）账单正常，之后（付款失败后的探测）上游回"账单不存在"。
	var detailCalls int
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/upgrade/upgrade_product_post":
			w.Write([]byte(`{"status":200}`))
		case "/upgrade/checkout_upgrade_product":
			w.Write([]byte(`{"status":200,"data":{"invoiceid":"9102"}}`))
		case "/get_invoices_detail":
			detailCalls++
			if detailCalls == 1 {
				w.Write([]byte(invoiceResp(`"50.00"`)))
				return
			}
			w.Write([]byte(`{"status":400,"msg":"账单不存在"}`))
		case "/apply_credit":
			w.Write([]byte(`{"status":400,"msg":"账单不存在"}`))
		default:
			http.NotFound(w, r)
		}
	})
	var p Provider
	ck := newMemCheckpoint()
	err := p.Upgrade(context.Background(), cfg, 5, server.UpgradeRequest{OrderID: 81, TargetPID: 402, Cycle: "monthly", DiffAmount: 50}, ck)
	if err == nil {
		t.Fatal("账单不存在应报错")
	}
	if !server.IsManualReview(err) {
		t.Fatalf("账单被删应转人工，实际: %v", err)
	}
	if got, ok, _ := ck.GetCheckpoint(ckUpgradeInvoice(81)); ok {
		t.Fatalf("失效账单的检查点应被清除，实得 %q", got)
	}
}