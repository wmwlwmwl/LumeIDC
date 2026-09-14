package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// fakeSnapshotProvider 只覆盖 checkRenewPrice 用到的能力；
// 嵌入 server.Provider 接口即可满足签名，其余方法在测试中不会被调用。
type fakeSnapshotProvider struct {
	server.Provider
	snap    server.ProductSnapshot
	snapErr error
}

func (f fakeSnapshotProvider) FetchProductSnapshot(context.Context, server.Config, int64) (server.ProductSnapshot, error) {
	return f.snap, f.snapErr
}

// insertRenewOrder 造一条带成本快照的订单，返回订单号；测试结束自动清理。
// snapshot 为 nil 时插入 NULL（模拟老数据无快照）。
func insertRenewOrder(t *testing.T, d *sql.DB, snapshot sql.NullString) int64 {
	t.Helper()
	ctx := context.Background()
	var uid, pid, psID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&uid); err != nil {
		t.Skip("库中暂无用户，跳过")
	}
	if err := d.QueryRowContext(ctx, `SELECT id FROM products LIMIT 1`).Scan(&pid); err != nil {
		t.Skip("库中暂无产品，跳过")
	}
	if err := d.QueryRowContext(ctx, `SELECT min(id) FROM pricesets`).Scan(&psID); err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	var oid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,config_snapshot)
		 VALUES($1,$2,$3,'monthly','11.00',1,$4) RETURNING id`,
		uid, pid, psID, snapshot).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM orders WHERE id=$1`, oid); err != nil {
			t.Errorf("清理测试订单失败: %v", err)
		}
	})
	return oid
}

const renewSnapshot10 = `{"quote":{"base":10,"total":10},"selection":{"mem":"2"}}`

// 续费前比价：上游偷偷涨价即转人工，且不产生任何上游账单（拦在建账单之前）。
func TestCheckRenewPrice(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	lc := &Lifecycle{db: d}
	s := &serviceRef{UpstreamPID: 1001}
	cfg := server.Config{}

	t.Run("上游价未变放行", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 10}}
		if err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid); err != nil {
			t.Fatalf("价格未变应放行，实得: %v", err)
		}
	})

	t.Run("上游偷偷涨价拦下并转人工", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 12}}
		err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid)
		var pce *server.PriceChangedError
		if !errors.As(err, &pce) {
			t.Fatalf("涨价应返回 PriceChangedError，实得: %v", err)
		}
		if pce.UpstreamAmount != 12 || pce.ExpectAmount != 10 {
			t.Fatalf("金额错误: 上游 %v 期望 %v", pce.UpstreamAmount, pce.ExpectAmount)
		}
		if pce.UpstreamPID != 1001 {
			t.Fatalf("应带回上游商品号，实得 %d", pce.UpstreamPID)
		}
		if !server.IsManualReview(err) {
			t.Fatal("应停止自动重试（标记人工处理）")
		}
	})

	t.Run("容差内的涨价放行", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 10.01}}
		if err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid); err != nil {
			t.Fatalf("涨价 0.01 在容差内应放行，实得: %v", err)
		}
	})

	t.Run("上游降价放行", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 9}}
		if err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid); err != nil {
			t.Fatalf("降价应放行，实得: %v", err)
		}
	})

	t.Run("按周期取上游价", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{
			String: `{"quote":{"total":100},"selection":{}}`, Valid: true})
		// 月付 10 元没超，但年付 200 远超下单时的 100 → 说明取的是所选周期。
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 10, Yearly: 200}}
		err := lc.checkRenewPrice(ctx, prov, cfg, s, "yearly", oid)
		var pce *server.PriceChangedError
		if !errors.As(err, &pce) {
			t.Fatalf("年付涨价应拦下，实得: %v", err)
		}
	})

	t.Run("读不到上游价转人工", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		prov := fakeSnapshotProvider{snapErr: errors.New("上游超时")}
		err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid)
		var mre *server.ManualReviewError
		if !errors.As(err, &mre) {
			t.Fatalf("读不到上游价应转人工，实得: %v", err)
		}
		var pce *server.PriceChangedError
		if errors.As(err, &pce) {
			t.Fatal("读不到上游价不得误判为价格变动")
		}
	})

	t.Run("无成本快照的老订单跳过比对", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{})
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 99}}
		if err := lc.checkRenewPrice(ctx, prov, cfg, s, "monthly", oid); err != nil {
			t.Fatalf("无快照应跳过比对，实得: %v", err)
		}
	})

	t.Run("供应商不支持快照时跳过", func(t *testing.T) {
		oid := insertRenewOrder(t, d, sql.NullString{String: renewSnapshot10, Valid: true})
		var plain nilProvider
		if err := lc.checkRenewPrice(ctx, plain, cfg, s, "monthly", oid); err != nil {
			t.Fatalf("供应商不支持时应跳过，实得: %v", err)
		}
	})
}

// nilProvider 满足 server.Provider 但不实现 ProductSnapshotFetcher。
type nilProvider struct{ server.Provider }

// fakeUpgradeProvider 覆盖 Lifecycle.Upgrade 需要的能力：HostUpgradeProvider + Code/Name。
type fakeUpgradeProvider struct {
	server.Provider
	err   error
	calls int
}

func (f *fakeUpgradeProvider) Code() string { return "fake-upgrade" }
func (f *fakeUpgradeProvider) Name() string { return "假上游-升级" }
func (f *fakeUpgradeProvider) Upgrade(context.Context, server.Config, int64, server.UpgradeRequest, server.CheckpointStore) error {
	f.calls++
	return f.err
}

// upgradeFixture 升级场景夹具：假上游服务器 + 源/目标产品 + "升级中"服务 + 已付升级订单（补差价 50）。
type upgradeFixture struct {
	svcID, orderID, pid, targetPid, userID, serverID int64
}

func setupUpgradeFixture(t *testing.T, d *sql.DB) upgradeFixture {
	t.Helper()
	ctx := context.Background()
	var fx upgradeFixture
	psID, err := repo.NewProducts(d).DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	// 专用用户：退款会写余额，不能用库里现成用户（会污染真实数据）。
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"upgtest-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&fx.userID); err != nil {
		t.Fatal(err)
	}
	var serverID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url) VALUES('单元测试-升级策略','fake-upgrade','http://127.0.0.1:1') RETURNING id`).
		Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	fx.serverID = serverID
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id,upstream_pid) VALUES(NULL,'单元测试-升级源',-1,$1,1001) RETURNING id`,
		serverID).Scan(&fx.pid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id,upstream_pid) VALUES(NULL,'单元测试-升级目标',-1,$1,2002) RETURNING id`,
		serverID).Scan(&fx.targetPid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,server_id,status,upstream_host_id,transition_state,cycle)
		 VALUES($1,$2,$3,1,555,'upgrading','monthly') RETURNING id`,
		fx.userID, fx.pid, serverID).Scan(&fx.svcID); err != nil {
		t.Fatal(err)
	}
	// 升级订单的 product_id 与 target_product_id 同为目标产品（对齐 CreateUpgradeOrder）；
	// orders.service_id 是升级单与服务的关联（后台重试/退款据此定位待处理升级）。
	if err := d.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,service_id,kind,target_product_id,diff_amount,config_snapshot)
		 VALUES($1,$2,$3,'monthly','50.00',1,$4,'upgrade',$2,'50.00','{}') RETURNING id`,
		fx.userID, fx.targetPid, psID, fx.svcID).Scan(&fx.orderID); err != nil {
		t.Fatal(err)
	}
	// 已支付账单（Refund 据此校验"订单已付"，gateway=balance 才退回余额）。
	if _, err := d.ExecContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,status,gateway,paid_at)
		 VALUES($1,$2,$3,'50.00',1,'balance',now())`,
		"UPGTEST-"+time.Now().Format("150405.000000000"), fx.userID, fx.orderID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM refunds WHERE order_id=$1`, fx.orderID},
			{`DELETE FROM balance_logs WHERE user_id=$1`, fx.userID},
			{`DELETE FROM invoices WHERE order_id=$1`, fx.orderID},
			{`DELETE FROM services WHERE id=$1`, fx.svcID},
			{`DELETE FROM orders WHERE id=$1`, fx.orderID},
			{`DELETE FROM products WHERE id=$1`, fx.targetPid},
			{`DELETE FROM products WHERE id=$1`, fx.pid},
			{`DELETE FROM servers WHERE id=$1`, serverID},
			{`DELETE FROM users WHERE id=$1`, fx.userID},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})
	return fx
}

// 升级失败的处置策略：上游涨价/等充值 = 保留账单与"升级中"不退款（管理员二选一）；
// 其它错误无法判断上游是否生效 = 回滚退款，保证用户不受损。
func TestUpgradeFailurePolicy(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupUpgradeFixture(t, d)
	svcID, orderID, pid, targetPid, uid := fx.svcID, fx.orderID, fx.pid, fx.targetPid, fx.userID

	prov := &fakeUpgradeProvider{}
	reg := server.NewRegistry()
	reg.Register(prov)
	lc := &Lifecycle{db: d, Servers: repo.NewServers(d), Products: repo.NewProducts(d),
		Providers: reg, Provisions: repo.NewProvisionRepo(d)}

	balance := func() float64 {
		t.Helper()
		var b float64
		if err := d.QueryRowContext(ctx, `SELECT balance::float8 FROM users WHERE id=$1`, uid).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	serviceState := func() (string, string, string) {
		t.Helper()
		var tr, pe, pd string
		if err := d.QueryRowContext(ctx,
			`SELECT coalesce(transition_state,''), coalesce(provision_error,''), coalesce(provision_data::text,'') FROM services WHERE id=$1`,
			svcID).Scan(&tr, &pe, &pd); err != nil {
			t.Fatal(err)
		}
		return tr, pe, pd
	}
	reset := func() {
		t.Helper()
		if _, err := d.ExecContext(ctx,
			`UPDATE services SET transition_state='upgrading', provision_error='', provision_data='{}'::jsonb,
			        product_id=$2 WHERE id=$1`, svcID, pid); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("上游涨价：不退款、保留升级中，等管理员决定", func(t *testing.T) {
		reset()
		before := balance()
		prov.err = &server.PriceChangedError{UpstreamAmount: 61, ExpectAmount: 50}
		if err := lc.Upgrade(ctx, svcID, "monthly", orderID); !server.IsManualReview(err) {
			t.Fatalf("涨价应转人工，实得: %v", err)
		}
		if got := balance(); got != before {
			t.Fatalf("涨价场景不应退款（退款后无法再按新价开通），余额 %v → %v", before, got)
		}
		tr, pe, _ := serviceState()
		if tr != "upgrading" {
			t.Fatalf("应保持升级中，实得 %q", tr)
		}
		if !strings.HasPrefix(pe, "升级失败：") {
			t.Fatalf("应在失败原因里留下待处理提示，实得 %q", pe)
		}
	})

	t.Run("上游余额不足：保持重试且不退款", func(t *testing.T) {
		reset()
		before := balance()
		prov.err = &server.RetryLaterError{Msg: "上游余额不足"}
		if err := lc.Upgrade(ctx, svcID, "monthly", orderID); !server.IsRetryLater(err) {
			t.Fatalf("余额不足应保持重试，实得: %v", err)
		}
		if got := balance(); got != before {
			t.Fatalf("等待充值期间不应退款，余额 %v → %v", before, got)
		}
		if tr, _, _ := serviceState(); tr != "upgrading" {
			t.Fatalf("应保持升级中以便充值后自动完成，实得 %q", tr)
		}
	})

	t.Run("未知错误：转人工核对，不退款保持升级中", func(t *testing.T) {
		reset()
		before := balance()
		prov.err = errors.New("上游 500")
		if err := lc.Upgrade(ctx, svcID, "monthly", orderID); !server.IsManualReview(err) {
			t.Fatalf("未知结果应转人工核对，实得: %v", err)
		}
		// 未知结果不能盲目退款：退了用户就没法再按新价"强制开通"，应隔离等对账恢复。
		if got := balance(); got != before {
			t.Fatalf("未知结果不应退款，余额 %v → %v", before, got)
		}
		tr, _, _ := serviceState()
		if tr != "upgrading" {
			t.Fatalf("应保持升级中等待对账恢复，实得 %q", tr)
		}
	})

	t.Run("成功：本地换产品并清掉失败提示", func(t *testing.T) {
		reset()
		if _, err := d.ExecContext(ctx, `UPDATE services SET provision_error='旧失败原因' WHERE id=$1`, svcID); err != nil {
			t.Fatal(err)
		}
		prov.err = nil
		if err := lc.Upgrade(ctx, svcID, "monthly", orderID); err != nil {
			t.Fatalf("上游成功应完成升级: %v", err)
		}
		var gotPid int64
		var tr, pe string
		if err := d.QueryRowContext(ctx,
			`SELECT product_id, coalesce(transition_state,''), coalesce(provision_error,'') FROM services WHERE id=$1`, svcID).
			Scan(&gotPid, &tr, &pe); err != nil {
			t.Fatal(err)
		}
		if gotPid != targetPid {
			t.Fatalf("应换成目标产品 %d，实得 %d", targetPid, gotPid)
		}
		if tr != "" || pe != "" {
			t.Fatalf("成功后应清除升级中与失败提示，实得 %q/%q", tr, pe)
		}
	})
}

// 后台处理待定升级的两个出口：重试（按上游新价开通）与退款（取消升级、服务保持原配置）。
func TestAdminUpgradeActions(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupUpgradeFixture(t, d)
	jobs := repo.NewFulfillmentJobs(d)
	pay := &Payment{db: d, Jobs: jobs, Provisions: repo.NewProvisionRepo(d)}
	// 清掉夹具里可能残留的履约任务，避免"已有任务在执行"挡住重试。
	if _, err := d.ExecContext(ctx, `DELETE FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}

	balance := func() float64 {
		t.Helper()
		var b float64
		if err := d.QueryRowContext(ctx, `SELECT balance::float8 FROM users WHERE id=$1`, fx.userID).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("重试升级：入队带订单号的 upgrade 任务", func(t *testing.T) {
		if err := pay.RetryUpgrade(ctx, fx.svcID); err != nil {
			t.Fatalf("重试应入队成功: %v", err)
		}
		var kind, cycle string
		var oid sql.NullInt64
		if err := d.QueryRowContext(ctx,
			`SELECT kind, cycle, order_id FROM fulfillment_jobs WHERE service_id=$1 ORDER BY id DESC LIMIT 1`, fx.svcID).
			Scan(&kind, &cycle, &oid); err != nil {
			t.Fatal(err)
		}
		if kind != "upgrade" {
			t.Fatalf("任务类型应为 upgrade，实得 %q", kind)
		}
		if !oid.Valid || oid.Int64 != fx.orderID {
			t.Fatalf("升级任务必须带原订单号 %d（否则找不到账单检查点），实得 %v", fx.orderID, oid)
		}
	})

	t.Run("退款：退回差价、作废订单、解除升级中且不重复退", func(t *testing.T) {
		before := balance()
		// 管理员 id 用夹具用户占位（测试库不保证存在 id=1 的管理员）。
		if err := pay.RefundUpgrade(ctx, fx.userID, fx.svcID, "上游涨价"); err != nil {
			t.Fatalf("退款应成功: %v", err)
		}
		if got := balance(); got != before+50 {
			t.Fatalf("应退回升级差价 50，余额 %v → %v", before, got)
		}
		var tr, pe string
		var orderStatus int16
		if err := d.QueryRowContext(ctx,
			`SELECT coalesce(sv.transition_state,''), coalesce(sv.provision_error,''), o.status
			   FROM services sv JOIN orders o ON o.id=$2 WHERE sv.id=$1`, fx.svcID, fx.orderID).
			Scan(&tr, &pe, &orderStatus); err != nil {
			t.Fatal(err)
		}
		if tr != "" || pe != "" {
			t.Fatalf("退款后应解除升级中并清失败提示，实得 %q/%q", tr, pe)
		}
		if orderStatus != 2 {
			t.Fatalf("升级订单应作废（status=2），实得 %d", orderStatus)
		}
		// 再次点击退款不得重复退（幂等），且因服务已非"升级中"直接拒绝。
		before2 := balance()
		if err := pay.RefundUpgrade(ctx, fx.userID, fx.svcID, "上游涨价"); err == nil {
			t.Fatal("已处理完的升级再次退款应被拒绝")
		}
		if got := balance(); got != before2 {
			t.Fatalf("重复退款不应再动余额，%v → %v", before2, got)
		}
	})
}

// renewFixture 续费场景夹具：服务已激活且续费待处理，队列里有一条人工复核的续费任务。
// expires_at 固定在月中，便于断言"退款撤回一个周期"（月末加减一个月在日历上有歧义）。
type renewFixture struct {
	svcID, orderID, productID, serverID, userID int64
}

func setupRenewFixture(t *testing.T, d *sql.DB) renewFixture {
	t.Helper()
	ctx := context.Background()
	var fx renewFixture
	psID, err := repo.NewProducts(d).DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"rwtest-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&fx.userID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url) VALUES('单元测试-续费处置','fake-upgrade','http://127.0.0.1:1') RETURNING id`).
		Scan(&fx.serverID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id,upstream_pid) VALUES(NULL,'单元测试-续费处置',-1,$1,1001) RETURNING id`,
		fx.serverID).Scan(&fx.productID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,server_id,status,upstream_host_id,transition_state,provision_error,cycle,expires_at)
		 VALUES($1,$2,$3,1,555,'renew_pending','续费失败：上游涨价','monthly','2027-06-15 12:00:00') RETURNING id`,
		fx.userID, fx.productID, fx.serverID).Scan(&fx.svcID); err != nil {
		t.Fatal(err)
	}
	// 已支付的续费订单（金额即本次续费实付）+ 对应账单。
	if err := d.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,service_id,kind,config_snapshot)
		 VALUES($1,$2,$3,'monthly','11.00',1,$4,'renew','{}') RETURNING id`,
		fx.userID, fx.productID, psID, fx.svcID).Scan(&fx.orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,status,gateway,paid_at)
		 VALUES($1,$2,$3,'11.00',1,'balance',now())`,
		"RWYTEST-"+time.Now().Format("150405.000000000"), fx.userID, fx.orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,status,attempts,dedupe_key)
		 VALUES($1,$2,'renew','monthly','manual_review',1,$3)`,
		fx.svcID, fx.orderID, "svc-renew-test:"+time.Now().Format("150405.000000000")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID},
			{`DELETE FROM refunds WHERE order_id=$1`, fx.orderID},
			{`DELETE FROM balance_logs WHERE user_id=$1`, fx.userID},
			{`DELETE FROM invoices WHERE order_id=$1`, fx.orderID},
			{`DELETE FROM services WHERE id=$1`, fx.svcID},
			{`DELETE FROM orders WHERE id=$1`, fx.orderID},
			{`DELETE FROM products WHERE id=$1`, fx.productID},
			{`DELETE FROM servers WHERE id=$1`, fx.serverID},
			{`DELETE FROM users WHERE id=$1`, fx.userID},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})
	return fx
}

// 后台处理待定续费的两个出口：重试（按上游新价续费）与退款（取消本次续费、撤回已延长的周期）。
func TestAdminRenewActions(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRenewFixture(t, d)
	jobs := repo.NewFulfillmentJobs(d)
	provisions := repo.NewProvisionRepo(d)
	pay := &Payment{db: d, Jobs: jobs, Provisions: provisions}

	state := func() (string, string, string, string) {
		t.Helper()
		var tr, pe, expires, ostatus string
		if err := d.QueryRowContext(ctx,
			`SELECT coalesce(sv.transition_state,''), coalesce(sv.provision_error,''),
			        to_char(sv.expires_at,'YYYY-MM-DD'), o.status::text
			   FROM services sv JOIN orders o ON o.id=$2 WHERE sv.id=$1`, fx.svcID, fx.orderID).
			Scan(&tr, &pe, &expires, &ostatus); err != nil {
			t.Fatal(err)
		}
		return tr, pe, expires, ostatus
	}
	balance := func() float64 {
		t.Helper()
		var b float64
		if err := d.QueryRowContext(ctx, `SELECT balance::float8 FROM users WHERE id=$1`, fx.userID).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("重试续费：写价格确认标记、入队带订单号的任务且保持锁定", func(t *testing.T) {
		if err := pay.RetryRenew(ctx, fx.svcID); err != nil {
			t.Fatalf("重试应入队成功: %v", err)
		}
		if v, ok, _ := provisions.GetCheckpoint(ctx, fx.svcID, renewPriceOkKey(fx.orderID)); !ok || v == "" {
			t.Fatal("应写入价格确认标记，否则重试又会被比价拦回人工")
		}
		var kind, cycle string
		var oid sql.NullInt64
		if err := d.QueryRowContext(ctx,
			`SELECT kind, cycle, order_id FROM fulfillment_jobs WHERE service_id=$1 ORDER BY id DESC LIMIT 1`, fx.svcID).
			Scan(&kind, &cycle, &oid); err != nil {
			t.Fatal(err)
		}
		if kind != "renew" || cycle != "monthly" {
			t.Fatalf("任务类型/周期错误: %s/%s", kind, cycle)
		}
		if !oid.Valid || oid.Int64 != fx.orderID {
			t.Fatalf("续费任务必须带原订单号 %d，实得 %v", fx.orderID, oid)
		}
		// 已确认价格的这次续费必须跳过续费前比价（否则管理员点了也还是被拦）。
		lc := &Lifecycle{db: d, Provisions: provisions}
		prov := fakeSnapshotProvider{snap: server.ProductSnapshot{Monthly: 99}}
		if err := lc.checkRenewPrice(ctx, prov, server.Config{}, &serviceRef{ID: fx.svcID, UpstreamPID: 1001}, "monthly", fx.orderID); err != nil {
			t.Fatalf("已确认价格应跳过比价，实得: %v", err)
		}
		tr, pe, _, _ := state()
		if tr != renewPendingState {
			t.Fatalf("续费未完成前应保持锁定，实得 %q", tr)
		}
		if pe != "" {
			t.Fatalf("应清掉失败提示避免按钮残留，实得 %q", pe)
		}
	})

	t.Run("退款：退实付、作废订单、撤回一个周期并解除锁定", func(t *testing.T) {
		before := balance()
		// 管理员 id 用夹具用户占位（测试库不保证存在 id=1 的管理员）。
		if err := pay.RefundRenew(ctx, fx.userID, fx.svcID, "上游涨价"); err != nil {
			t.Fatalf("退款应成功: %v", err)
		}
		if got := balance(); got != before+11 {
			t.Fatalf("应退回续费实付 11，余额 %v → %v", before, got)
		}
		tr, pe, expires, ostatus := state()
		if tr != "" || pe != "" {
			t.Fatalf("退款后应解除续费待处理，实得 %q/%q", tr, pe)
		}
		if ostatus != "2" {
			t.Fatalf("续费订单应作废（status=2），实得 %s", ostatus)
		}
		if expires != "2027-05-15" {
			t.Fatalf("应撤回已延长的一个月（2027-06-15 → 2027-05-15），实得 %s", expires)
		}
		// 终态检查点必须落库：排队中的续费任务再跑时要据此短路，否则会继续往上游续期。
		if v, ok, _ := provisions.GetCheckpoint(ctx, fx.svcID, renewDoneCkKey(fx.orderID)); !ok || v != "refunded" {
			t.Fatalf("应写入续费退款终态检查点，实得 %q,%v", v, ok)
		}
		// 再次点击退款不得重复退。
		before2 := balance()
		if err := pay.RefundRenew(ctx, fx.userID, fx.svcID, "上游涨价"); err == nil {
			t.Fatal("已处理完的续费再次退款应被拒绝")
		}
		if got := balance(); got != before2 {
			t.Fatalf("重复退款不应再动余额，%v → %v", before2, got)
		}
	})
}
