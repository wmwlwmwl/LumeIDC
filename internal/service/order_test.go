package service

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"lumeidc/internal/repo"
)

func TestApplyProfit(t *testing.T) {
	cases := []struct {
		cost  float64
		ptype int16
		pval  float64
		want  float64
	}{
		{7.00, 0, 100, 14.00},     // 百分比 100%：成本翻倍
		{7.00, 0, 50, 10.50},      // 百分比 50%
		{7.00, 0, 0, 7.00},        // 0 不加成
		{7.00, 0, -10, 7.00},      // 负值不加成（防负价）
		{7.00, 1, 3, 10.00},       // 固定金额 +3
		{0.00, 0, 100, 0.00},      // 成本 0（纯配置计价产品的季付路径不会走到，但求稳）
		{12.34, 0, 12.5, 13.8825}, // 非整值不在此四舍五入（mathRound 由调用方负责）
	}
	for i, c := range cases {
		if got := applyProfit(c.cost, c.ptype, c.pval); got != c.want {
			t.Errorf("case %d: applyProfit(%v,%d,%v)=%v want %v", i, c.cost, c.ptype, c.pval, got, c.want)
		}
	}
}

// 首购要收上游一次性初装费（并与周期费一起参与利润加成），续费不收。
// 之前同步丢掉了初装费，本地售价低于上游成本，每笔首购都亏这笔钱。
func TestCreateOrderChargesSetupFeeOnce(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	var uid, typeID, pid, svcID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"setupfee-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO product_types(name) VALUES('单元测试-初装费') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	// 成本：基础价 3 + CPU 10 + 带宽 10、带宽另收一次性初装费 5；利润百分比 20%
	cfgOpts := `[{"field":"cpu","name":"CPU","option_mode":"select","required":true,"sub":[{"name":"2核","pricing":{"monthly":10}}]},
	 {"field":"bw","name":"带宽","option_mode":"range","min":1,"max":100,"step":1,"unit":"M",
	  "sub":[{"name":"10M带宽","value":"10","min":10,"max":10,"pricing":{"monthly":10},"setup":{"monthly":5,"quarterly":0,"yearly":0}}]}]`
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,requires_identity,profit_type,profit_value,configoption)
		 VALUES($1,'单元测试-初装费',-1,false,0,20,$2::jsonb) RETURNING id`, typeID, cfgOpts).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,3,0,0)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status,cycle,expires_at) VALUES($1,$2,1,'monthly',now()) RETURNING id`,
		uid, pid).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM invoices WHERE user_id=$1`, uid},
			{`DELETE FROM orders WHERE user_id=$1`, uid},
			{`DELETE FROM services WHERE id=$1`, svcID},
			{`DELETE FROM product_prices WHERE product_id=$1`, pid},
			{`DELETE FROM products WHERE id=$1`, pid},
			{`DELETE FROM product_types WHERE id=$1`, typeID},
			{`DELETE FROM users WHERE id=$1`, uid},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})

	orders := &Orders{db: d, Products: products}
	sel := map[string]string{"cpu": "2核", "bw": "10"}

	// 首购：(3+10+10+5) × 1.2 = 33.60；漏掉初装费只有 27.60
	orderID, _, amount, err := orders.CreateOrder(ctx, uid, pid, psID, "monthly", sel, "")
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if amount != "33.60" {
		t.Fatalf("首购应含初装费：期望 33.60，实得 %s", amount)
	}
	// 绑定首购订单，续费据此取初购配置（真实链路里由支付时建服务完成）
	if _, err := d.ExecContext(ctx, `UPDATE services SET order_id=$2 WHERE id=$1`, svcID, orderID); err != nil {
		t.Fatal(err)
	}

	// 续费：(3+10+10) × 1.2 = 27.60，初装费是一次性的，不再收
	_, _, renewAmount, err := orders.CreateRenewOrder(ctx, uid, svcID, "monthly")
	if err != nil {
		t.Fatalf("续费下单失败: %v", err)
	}
	if renewAmount != "27.60" {
		t.Fatalf("续费不该含初装费：期望 27.60，实得 %s", renewAmount)
	}
}

// 免费商品（月付价 0）也必须能续费：此前无条件拒绝 0 元金额，用户点续费只看到"续费金额无效"。
// 但季/年付价为 0 属于"未配置该周期"，仍要拦下（防止改 POST 用未配置周期 0 元续一年）。
func TestCreateRenewOrderFreeProduct(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	var uid, pid, svcID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"freerenew-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,requires_identity) VALUES(NULL,'单元测试-免费续费',-1,false) RETURNING id`).
		Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,0,0,0)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status,cycle,expires_at) VALUES($1,$2,1,'monthly',now()) RETURNING id`,
		uid, pid).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM invoices WHERE user_id=$1`, uid},
			{`DELETE FROM orders WHERE user_id=$1`, uid},
			{`DELETE FROM services WHERE id=$1`, svcID},
			{`DELETE FROM product_prices WHERE product_id=$1`, pid},
			{`DELETE FROM products WHERE id=$1`, pid},
			{`DELETE FROM users WHERE id=$1`, uid},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})

	orders := &Orders{db: d, Products: products}

	t.Run("月付价 0 的免费商品可续费且金额为 0", func(t *testing.T) {
		_, invID, amount, err := orders.CreateRenewOrder(ctx, uid, svcID, "monthly")
		if err != nil {
			t.Fatalf("免费商品应能续费，实得: %v", err)
		}
		if amount != "0.00" || invID <= 0 {
			t.Fatalf("应生成 0 元账单，实得 amount=%q invoice=%d", amount, invID)
		}
	})

	t.Run("未配置季付价的周期仍拒绝", func(t *testing.T) {
		_, _, _, err := orders.CreateRenewOrder(ctx, uid, svcID, "quarterly")
		if err == nil || !strings.Contains(err.Error(), "未提供所选计费周期") {
			t.Fatalf("未配置周期应拦下并给出可读原因，实得: %v", err)
		}
	})
}

// 累计消费口径：排除充值账单（充值只是余额入账，充 100 再花 100 不该显示 200），
// 并扣减已退款额（退款把已付金额退回用户，不构成消费）。前台概览与后台用户编辑共用此统计。
func TestUserStatsSpendingScope(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	var uid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"userstats-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM refunds WHERE user_id=$1`,
			`DELETE FROM invoices WHERE user_id=$1`,
			`DELETE FROM users WHERE id=$1`,
		} {
			if _, err := d.ExecContext(context.Background(), q, uid); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q, err)
			}
		}
	})
	// 已付充值 100（应排除）、已付订单 30（应计入）、未付订单 50（不应计入）
	// paid_amount 为 NOT NULL DEFAULT 0（030 迁移），已付账单须显式写入实付金额
	for _, inv := range []struct {
		kind   string
		amount string
		paid   string
		status int
	}{
		{"recharge", "100.00", "100.00", 1},
		{"order", "30.00", "30.00", 1},
		{"order", "50.00", "0", 0},
	} {
		no, err := genInvoiceNo()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.ExecContext(ctx,
			`INSERT INTO invoices(no,user_id,amount,paid_amount,kind,status) VALUES($1,$2,$3,$4,$5,$6)`,
			no, uid, inv.amount, inv.paid, inv.kind, inv.status); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewServicesRepo(d)
	spent := func() float64 {
		t.Helper()
		_, _, _, paidTotal := svc.UserStats(ctx, uid)
		v, err := strconv.ParseFloat(paidTotal, 64)
		if err != nil {
			t.Fatalf("累计消费不是合法数字: %q (%v)", paidTotal, err)
		}
		return v
	}

	if got := spent(); got != 30 {
		t.Fatalf("累计消费应只含已付订单 30、排除已付充值与未付订单，实得 %v", got)
	}

	// 退款 10 后应扣减（refunds.order_id 无外键，统计只按 user_id + status 汇总）
	if _, err := d.ExecContext(ctx,
		`INSERT INTO refunds(user_id,order_id,amount,method,status) VALUES($1,0,'10.00','balance','done')`,
		uid); err != nil {
		t.Fatal(err)
	}
	if got := spent(); got != 20 {
		t.Fatalf("已退款 10 应从累计消费扣减，期望 20，实得 %v", got)
	}
}
