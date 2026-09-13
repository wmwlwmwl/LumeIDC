package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// fakeUpstream 内存假上游：实现 server.Provider + ProductSnapshotFetcher + CatalogLister。
type fakeUpstream struct {
	snap       server.ProductSnapshot
	snapErr    error
	catalog    []server.UpstreamProduct
	catalogErr error
	snapCalls  int // 单商品快照调用次数
}

func (f *fakeUpstream) Code() string                                        { return "fake" }
func (f *fakeUpstream) Name() string                                        { return "假上游" }
func (f *fakeUpstream) TestConnection(context.Context, server.Config) error { return nil }
func (f *fakeUpstream) Catalog(context.Context, server.Config) ([]server.UpstreamProduct, error) {
	return f.catalog, f.catalogErr
}
func (f *fakeUpstream) Provision(context.Context, server.Config, server.ProvisionRequest, server.CheckpointStore) (server.ProvisionResult, error) {
	return server.ProvisionResult{}, nil
}
func (f *fakeUpstream) Renew(context.Context, server.Config, int64, string, server.CheckpointStore) error {
	return nil
}
func (f *fakeUpstream) Suspend(context.Context, server.Config, int64) error   { return nil }
func (f *fakeUpstream) Unsuspend(context.Context, server.Config, int64) error { return nil }
func (f *fakeUpstream) Terminate(context.Context, server.Config, int64) error { return nil }
func (f *fakeUpstream) Status(context.Context, server.Config, int64) (server.ServiceStatus, error) {
	return server.ServiceStatus{}, nil
}
func (f *fakeUpstream) CatalogLight(context.Context, server.Config) ([]server.UpstreamProduct, error) {
	return f.catalog, f.catalogErr
}
func (f *fakeUpstream) FetchProductSnapshot(context.Context, server.Config, int64) (server.ProductSnapshot, error) {
	f.snapCalls++
	return f.snap, f.snapErr
}

// TestUpstreamGuardVerifyBeforeOrder 自检下单前上游价格校验的四条判定路径。
// 需要 DB（TEST_DATABASE_DSN）；上游用内存 fake，不依赖真实魔方财务。
func TestUpstreamGuardVerifyBeforeOrder(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()

	products := repo.NewProducts(d)
	servers := repo.NewServers(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Fatalf("读取默认价格组失败: %v", err)
	}

	fake := &fakeUpstream{}
	reg := server.NewRegistry()
	reg.Register(fake)
	guard := &UpstreamGuard{Servers: servers, Products: products, Providers: reg}

	// 夹具：假上游服务器 + 绑定产品（本地月价 10、利润 10% → 售价 11）
	var serverID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,api_username,api_key) VALUES('单元测试-下单校验','fake','http://127.0.0.1:1','','') RETURNING id`).
		Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	var pid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id,upstream_pid,profit_type,profit_value)
		 VALUES(NULL,'单元测试-下单校验',-1,$1,1001,0,10) RETURNING id`, serverID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// 先删产品（级联 product_prices）再删服务器，避免外键残留。
		if _, err := d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, pid); err != nil {
			t.Errorf("清理测试产品失败: %v", err)
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM servers WHERE id=$1`, serverID); err != nil {
			t.Errorf("清理测试服务器失败: %v", err)
		}
	}()
	setLocal := func(monthly float64) {
		t.Helper()
		if _, err := d.ExecContext(ctx,
			`UPDATE product_prices SET monthly=$3 WHERE product_id=$1 AND priceset_id=$2`, pid, psID, monthly); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,10,100,1000)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}

	inCatalog := []server.UpstreamProduct{{PID: 1001, Name: "测试商品"}}

	t.Run("上游价与本地一致时不拦截", func(t *testing.T) {
		setLocal(10)
		fake.snap = server.ProductSnapshot{Monthly: 10, Quarterly: 100, Yearly: 1000}
		fake.snapErr, fake.catalogErr = nil, nil
		fake.catalog = inCatalog
		if err := guard.VerifyBeforeOrder(ctx, pid, "monthly", nil); err != nil {
			t.Fatalf("本地售价 11 ≥ 上游成本 10，应放行，实得: %v", err)
		}
	})

	t.Run("本地售价低于上游成本时同步并拒绝", func(t *testing.T) {
		setLocal(10) // 本地售价 11
		fake.snap = server.ProductSnapshot{Monthly: 12, Quarterly: 100, Yearly: 1000}
		fake.snapErr, fake.catalogErr = nil, nil
		fake.catalog = inCatalog
		err := guard.VerifyBeforeOrder(ctx, pid, "monthly", nil)
		if !errors.Is(err, ErrUpstreamPriceChanged) {
			t.Fatalf("应返回 ErrUpstreamPriceChanged，实得: %v", err)
		}
		var monthly string
		if err := d.QueryRowContext(ctx,
			`SELECT monthly::text FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, pid, psID).
			Scan(&monthly); err != nil {
			t.Fatal(err)
		}
		if monthly != "12.00" {
			t.Fatalf("命中后应把本地价同步为上游新价 12.00，实得 %s", monthly)
		}
	})

	t.Run("上游初装费计入比价", func(t *testing.T) {
		// 本地配置项无初装费、上游同价但另收 5 元初装费：
		// 本地：10 基础 + 10 带宽 = 20 → 售价 22；上游：20 + 5 = 25 > 22 → 必须拦下。
		// 若漏算初装费（按 20 比）就会放行，用户按 22 付款而上游要 25。
		const localOpts = `[{"field":"bw","name":"带宽","option_mode":"range","min":1,"max":100,"step":1,
		   "sub":[{"name":"10M带宽","value":"10","min":10,"max":10,"pricing":{"monthly":10}}]}]`
		const upOpts = `[{"field":"bw","name":"带宽","option_mode":"range","min":1,"max":100,"step":1,
		   "sub":[{"name":"10M带宽","value":"10","min":10,"max":10,"pricing":{"monthly":10},
		          "setup":{"monthly":5,"quarterly":0,"yearly":0}}]}]`
		if _, err := d.ExecContext(ctx, `UPDATE products SET configoption=$2::jsonb WHERE id=$1`, pid, localOpts); err != nil {
			t.Fatal(err)
		}
		var upOptList []repo.ConfigOption
		if err := json.Unmarshal([]byte(upOpts), &upOptList); err != nil {
			t.Fatal(err)
		}
		setLocal(10)
		fake.snap = server.ProductSnapshot{Monthly: 10, Quarterly: 100, Yearly: 1000, ConfigOptions: upOptList}
		fake.snapErr, fake.catalogErr = nil, nil
		fake.catalog = inCatalog
		err := guard.VerifyBeforeOrder(ctx, pid, "monthly", map[string]string{"bw": "10"})
		if !errors.Is(err, ErrUpstreamPriceChanged) {
			t.Fatalf("上游含初装费时成本 25 > 售价 22，应拦下，实得: %v", err)
		}
		if _, err := d.ExecContext(ctx, `UPDATE products SET configoption='[]'::jsonb WHERE id=$1`, pid); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("上游不可用时拒绝下单", func(t *testing.T) {
		setLocal(10)
		fake.snapErr = errors.New("上游超时")
		fake.catalog = inCatalog
		err := guard.VerifyBeforeOrder(ctx, pid, "monthly", nil)
		fake.snapErr = nil
		if !errors.Is(err, ErrUpstreamUnavailable) {
			t.Fatalf("应返回 ErrUpstreamUnavailable，实得: %v", err)
		}
	})

	t.Run("上游目录中已无该商品时下架并拒绝", func(t *testing.T) {
		setLocal(10)
		fake.snap = server.ProductSnapshot{Monthly: 10, Quarterly: 100, Yearly: 1000}
		fake.catalog = []server.UpstreamProduct{{PID: 9999, Name: "别的商品"}}
		err := guard.VerifyBeforeOrder(ctx, pid, "monthly", nil)
		if !errors.Is(err, ErrUpstreamUnshelved) {
			t.Fatalf("应返回 ErrUpstreamUnshelved，实得: %v", err)
		}
		var reason string
		if err := d.QueryRowContext(ctx,
			`SELECT upstream_offline_reason FROM products WHERE id=$1`, pid).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		if reason != repo.UpstreamOfflineUnshelved {
			t.Fatalf("应标记 %q，实得 %q", repo.UpstreamOfflineUnshelved, reason)
		}
		if _, err := d.ExecContext(ctx,
			`UPDATE products SET upstream_offline_reason='' WHERE id=$1`, pid); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("未绑定上游的产品不做校验也不请求上游", func(t *testing.T) {
		var plainID int64
		if err := d.QueryRowContext(ctx,
			`INSERT INTO products(type_id,name,stock) VALUES(NULL,'单元测试-未绑定',-1) RETURNING id`).Scan(&plainID); err != nil {
			t.Fatal(err)
		}
		defer d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, plainID)
		before := fake.snapCalls
		if err := guard.VerifyBeforeOrder(ctx, plainID, "monthly", nil); err != nil {
			t.Fatalf("未绑定上游应放行，实得: %v", err)
		}
		if fake.snapCalls != before {
			t.Fatalf("未绑定上游不应请求上游，快照调用次数 %d → %d", before, fake.snapCalls)
		}
	})
}

// 升级下单前也做上游实时比价：目标商品上游涨价时拦下本次升级（本地价已同步），
// 且不落任何升级单——否则差价会按旧价算、页面显示与实际收款不一致。
func TestCreateUpgradeOrderVerifiesUpstreamPrice(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupUpgradeFixture(t, d)
	psID, err := repo.NewProducts(d).DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	// 目标商品要有本地价，否则比价无从比较（guard 会直接放行）。
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,10,100,1000)`,
		fx.targetPid, psID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.ExecContext(context.Background(),
			`DELETE FROM product_prices WHERE product_id=$1`, fx.targetPid); err != nil {
			t.Errorf("清理目标商品价格失败: %v", err)
		}
	})
	// 建升级单要求服务不在过渡状态；上游改用 fake 供应商（fakeUpstream 的 code）。
	if _, err := d.ExecContext(ctx, `UPDATE services SET transition_state='' WHERE id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE servers SET provider='fake' WHERE id=$1`, fx.serverID); err != nil {
		t.Fatal(err)
	}

	fake := &fakeUpstream{
		snap:    server.ProductSnapshot{Monthly: 20, Quarterly: 200, Yearly: 2000},
		catalog: []server.UpstreamProduct{{PID: 2002, Name: "目标商品"}},
	}
	reg := server.NewRegistry()
	reg.Register(fake)
	products := repo.NewProducts(d)
	orders := &Orders{db: d, Products: products,
		Upstream: &UpstreamGuard{Servers: repo.NewServers(d), Products: products, Providers: reg}}

	_, _, _, _, err = orders.CreateUpgradeOrder(ctx, fx.userID, fx.svcID, fx.targetPid, "monthly", nil)
	if !errors.Is(err, ErrUpstreamPriceChanged) {
		t.Fatalf("上游涨价应拦下升级并要求重新确认，实得: %v", err)
	}
	var n int
	if err := d.QueryRowContext(ctx,
		`SELECT count(*) FROM orders WHERE service_id=$1 AND kind='upgrade'`, fx.svcID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 { // 夹具里那条已付升级单
		t.Fatalf("被拦下时不应新建升级单，实得 %d 条", n)
	}
}
