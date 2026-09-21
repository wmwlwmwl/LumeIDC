package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

func TestApplyPromotionRejectsUnsupportedTypes(t *testing.T) {
	service := &PromotionService{}
	for _, promoType := range []string{"bogo", "group_buy", "unknown"} {
		t.Run(promoType, func(t *testing.T) {
			if err := service.ValidatePromotionType(promoType); err == nil {
				t.Fatalf("未实现活动类型 %q 必须拒绝", promoType)
			}
		})
	}
}

func TestApplyPromotionRuleMatrix(t *testing.T) {
	cases := []struct {
		name     string
		promo    *ActivePromotion
		sell     float64
		want     float64
		discount float64
	}{
		{name: "discount", promo: &ActivePromotion{Type: "discount", Price: 80}, sell: 100, want: 80, discount: 20},
		{name: "flash_sale", promo: &ActivePromotion{Type: "flash_sale", Price: 60}, sell: 100, want: 60, discount: 40},
		{name: "full_reduction", promo: &ActivePromotion{Type: "full_reduction", Threshold: 100, Reduce: 20}, sell: 100, want: 80, discount: 20},
		{name: "new_user", promo: &ActivePromotion{Type: "new_user", Price: 70}, sell: 100, want: 70, discount: 30},
		{name: "coupon_giveaway", promo: &ActivePromotion{Type: "coupon_giveaway", CouponID: 9}, sell: 100, want: 100, discount: 0},
		{name: "full_reduction_cannot_exceed_sell", promo: &ActivePromotion{Type: "full_reduction", Threshold: 10, Reduce: 80}, sell: 50, want: 0, discount: 50},
	}
	service := &PromotionService{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, discount := service.ApplyPromotion(tc.sell, tc.promo)
			if got != tc.want || discount != tc.discount {
				t.Fatalf("ApplyPromotion()=(%v,%v), want (%v,%v)", got, discount, tc.want, tc.discount)
			}
		})
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
	orderID, _, amount, err := orders.CreateOrder(ctx, uid, pid, psID, "monthly", sel, "", 0, 0)
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
	_, _, renewAmount, err := orders.CreateRenewOrder(ctx, uid, svcID, "monthly", "")
	if err != nil {
		t.Fatalf("续费下单失败: %v", err)
	}
	if renewAmount != "27.60" {
		t.Fatalf("续费不该含初装费：期望 27.60，实得 %s", renewAmount)
	}
}

// 免费商品（三周期价均为 0）必须能续费：此前无条件拒绝 0 元金额，用户点续费只看到"续费金额无效"。
// 注意：三个周期价全 0 时季付/年付同样放行，各周期都以 0 元续期（本用例固定该行为）。
// 原因：product_prices 三列都是 NOT NULL DEFAULT 0，「未配置该周期」与「该周期 0 元」在数据模型上
// 无法区分；若收紧成「非月付且无正价即拒绝」，靠配置项计价的纯配置计价产品会连带失去季付/年付入口。
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
		_, invID, amount, err := orders.CreateRenewOrder(ctx, uid, svcID, "monthly", "")
		if err != nil {
			t.Fatalf("免费商品应能续费，实得: %v", err)
		}
		if amount != "0.00" || invID <= 0 {
			t.Fatalf("应生成 0 元账单，实得 amount=%q invoice=%d", amount, invID)
		}
	})

	t.Run("三周期价全 0 的免费商品季付也可续费", func(t *testing.T) {
		_, invID, amount, err := orders.CreateRenewOrder(ctx, uid, svcID, "quarterly", "")
		if err != nil {
			t.Fatalf("免费商品应能季付续费，实得: %v", err)
		}
		if amount != "0.00" || invID <= 0 {
			t.Fatalf("应生成 0 元账单，实得 amount=%q invoice=%d", amount, invID)
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

// 新客活动命中老用户时必须「跳过活动按原价下单」，不能拒绝下单：
// 旧实现在这里直接返回 ErrPromotionNotApplicable，导致挂了新客活动的商品对老用户完全不可购买。
func TestCreateOrderSkipsNewUserPromotionForExistingUser(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	var uid, typeID, pid, promoID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"promoolduser-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO product_types(name) VALUES('单元测试-新客活动') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,requires_identity) VALUES($1,'单元测试-新客活动商品',-1,false) RETURNING id`,
		typeID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,100,0,0)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	// 老用户：先有一笔历史订单，IsNewUser 即为 false
	if _, err := d.ExecContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status) VALUES($1,$2,$3,'monthly',100,1)`,
		uid, pid, psID); err != nil {
		t.Fatal(err)
	}
	// 新客专享活动价 1 元（远低于原价 100）
	if err := d.QueryRowContext(ctx,
		`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		 VALUES('单元测试-新客专享','new_user',now()-interval '1 day',now()+interval '1 day',true,0) RETURNING id`).
		Scan(&promoID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO promotion_products(promotion_id,product_id,priceset_id,cycle,rules)
		 VALUES($1,$2,$3,'monthly','{"price":1}'::jsonb)`, promoID, pid, psID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// 顺序照顾外键：活动（级联 promotion_products）→ 账单/订单 → 价格 → 商品 → 类型 → 用户
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM invoices WHERE user_id=$1`, uid},
			{`DELETE FROM orders WHERE user_id=$1`, uid},
			{`DELETE FROM promotions WHERE id=$1`, promoID},
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

	orders := &Orders{db: d, Products: products,
		Promotion: NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))}

	// 夹具前提自检：活动必须真的命中，且必须真的被判为「不适用于该用户」。
	// 少了这两条断言，活动若没命中（ap=nil）用例也会通过，等于没覆盖本次修复的分支。
	ap, apErr := orders.Promotion.ActivePromotionFor(ctx, pid, psID, "monthly")
	if apErr != nil || ap == nil {
		t.Fatalf("夹具前提不成立：活动应命中该商品，实得 ap=%+v err=%v", ap, apErr)
	}
	if applicableErr := orders.Promotion.ValidatePromotionApplicable(ctx, ap, uid); !errors.Is(applicableErr, ErrPromotionNotApplicable) {
		t.Fatalf("夹具前提不成立：老用户应被判为不适用，实得 %v", applicableErr)
	}

	_, _, amount, err := orders.CreateOrder(ctx, uid, pid, psID, "monthly", map[string]string{}, "", 0, 0)
	if err != nil {
		t.Fatalf("新客活动对老用户应跳过活动、按原价下单，实得错误: %v", err)
	}
	if amount != "100.00" {
		t.Fatalf("应按原价 100.00 下单（而非活动价 1.00），实得 %s", amount)
	}
}

// coupon_giveaway 活动发放的专属券不可用（未领取/已用完/已过期）时必须按原价继续下单：
// 旧实现直接返回优惠码校验错误，导致挂了该活动的商品对该用户永久无法下单
// （专属券 usage_limit=1，用户用掉一次后就再也买不了）。
func TestCreateOrderFallsBackWhenGiveawayCouponUnavailable(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	suffix := time.Now().Format("150405.000000000")
	var uid, typeID, pid, promoID, tplCouponID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"giveaway-"+suffix+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO product_types(name) VALUES('单元测试-发券活动') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,requires_identity) VALUES($1,'单元测试-发券活动商品',-1,false) RETURNING id`,
		typeID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,100,0,0)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	// 活动模板券：用户「领取」后才会生成 PROMO_{活动}_{用户} 的专属码，本用例刻意不领取。
	if err := d.QueryRowContext(ctx,
		`INSERT INTO coupons(code,type,value,min_amount,usage_limit,active) VALUES($1,'fixed',30,0,0,true) RETURNING id`,
		"单元测试-模板券-"+suffix).Scan(&tplCouponID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		 VALUES('单元测试-发券活动','coupon_giveaway',now()-interval '1 day',now()+interval '1 day',true,0) RETURNING id`).
		Scan(&promoID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO promotion_products(promotion_id,product_id,priceset_id,cycle,rules)
		 VALUES($1,$2,$3,'monthly',$4::jsonb)`, promoID, pid, psID, fmt.Sprintf(`{"coupon_id":%d}`, tplCouponID)); err != nil {
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
			{`DELETE FROM promotions WHERE id=$1`, promoID},
			{`DELETE FROM coupons WHERE id=$1`, tplCouponID},
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

	orders := &Orders{db: d, Products: products,
		Promotion: NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))}

	// 夹具前提自检：必须真的命中 coupon_giveaway 活动，且专属码确实不存在。
	// 少了这两条，活动没命中（ap=nil）时用例同样会通过，等于没覆盖修复的分支。
	ap, apErr := orders.Promotion.ActivePromotionFor(ctx, pid, psID, "monthly")
	if apErr != nil || ap == nil || ap.Type != "coupon_giveaway" {
		t.Fatalf("夹具前提不成立：应命中 coupon_giveaway 活动，实得 ap=%+v err=%v", ap, apErr)
	}
	var couponExists bool
	if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coupons WHERE code=$1)`,
		fmt.Sprintf("PROMO_%d_%d", promoID, uid)).Scan(&couponExists); err != nil {
		t.Fatal(err)
	}
	if couponExists {
		t.Fatal("夹具前提不成立：专属券不应存在（本用例模拟未领取）")
	}

	_, _, amount, err := orders.CreateOrder(ctx, uid, pid, psID, "monthly", map[string]string{}, "", 0, 0)
	if err != nil {
		t.Fatalf("专属券不可用时应按原价下单，实得错误: %v", err)
	}
	if amount != "100.00" {
		t.Fatalf("应按原价 100.00 下单，实得 %s", amount)
	}
}

// 活动结束或被禁用后，服务端必须拒绝携带该活动绑定的新订单（不落单、不改判其他活动）。
// 前端隐藏入口只是体验层，这里保证绕过前端直接带参下单也拿不到活动价。
func TestCreateOrderRejectsEndedOrDisabledPromotion(t *testing.T) {
	cases := []struct {
		name    string
		endsAt  string
		enabled bool
	}{
		{"已结束", `now()-interval '1 hour'`, true},
		{"已禁用", `now()+interval '1 day'`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDB(t)
			ctx := context.Background()
			products := repo.NewProducts(d)
			psID, err := products.DefaultPricesetID(ctx)
			if err != nil {
				t.Skip("库中暂无价格组，跳过")
			}
			var uid, typeID, pid, promoID, bindingID int64
			if err := d.QueryRowContext(ctx,
				`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
				"promoended-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRowContext(ctx,
				`INSERT INTO product_types(name) VALUES($1) RETURNING id`,
				"单元测试-结束活动-"+time.Now().Format("150405.000000000")).Scan(&typeID); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRowContext(ctx,
				`INSERT INTO products(type_id,name,stock,requires_identity) VALUES($1,$2,-1,false) RETURNING id`,
				typeID, "单元测试-结束活动商品-"+time.Now().Format("150405.000000000")).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ExecContext(ctx,
				`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,100,0,0)`,
				pid, psID); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRowContext(ctx, fmt.Sprintf(
				`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
				 VALUES($1,'discount',now()-interval '1 day',%s,$2,0) RETURNING id`,
				c.endsAt), "单元测试-结束折扣-"+time.Now().Format("150405.000000000"), c.enabled).Scan(&promoID); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRowContext(ctx,
				`INSERT INTO promotion_products(promotion_id,product_id,priceset_id,cycle,rules)
				 VALUES($1,$2,$3,'monthly','{"price":1}'::jsonb) RETURNING id`,
				promoID, pid, psID).Scan(&bindingID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx := context.Background()
				// 顺序照顾外键：活动（级联绑定）→ 账单/订单 → 价格 → 商品 → 类型 → 用户
				for _, q := range []struct {
					query string
					arg   int64
				}{
					{`DELETE FROM invoices WHERE user_id=$1`, uid},
					{`DELETE FROM orders WHERE user_id=$1`, uid},
					{`DELETE FROM promotions WHERE id=$1`, promoID},
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

			orders := &Orders{db: d, Products: products,
				Promotion: NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))}

			// 夹具前提自检：该活动确实没有生效绑定可供采纳。
			if ap, apErr := orders.Promotion.ActivePromotionForRequested(ctx, pid, psID, "monthly", promoID, bindingID); apErr == nil && ap != nil {
				t.Fatal("夹具前提不成立：结束/禁用的活动不应存在生效绑定")
			}

			orderID, _, _, err := orders.CreateOrder(ctx, uid, pid, psID, "monthly", map[string]string{}, "", promoID, bindingID)
			if !errors.Is(err, ErrPromotionBindingMismatch) {
				t.Fatalf("结束/禁用的活动应拒绝下单，实得 orderID=%d err=%v", orderID, err)
			}
			var n int
			if err := d.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE user_id=$1`, uid).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("拒绝下单时不得落单")
			}
		})
	}
}

// 续费支持优惠码（080 场景化）：renew/both 券可抵扣续费金额；new 券被场景拒绝且不落单。
func TestCreateRenewOrderWithCoupon(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	coupons := repo.NewCoupons(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	stamp := time.Now().Format("150405.000000000")
	var uid, pid, svcID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"renewcoupon-"+stamp+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,requires_identity) VALUES(NULL,'单元测试-续费用券',-1,false) RETURNING id`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,100,0,0)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status,cycle,expires_at) VALUES($1,$2,1,'monthly',now()) RETURNING id`,
		uid, pid).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	renewCode := "T-RENEW-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10)
	newCode := "T-NEW-" + strconv.FormatInt(time.Now().UnixNano()%100000000, 10)
	if err := coupons.CreateWithOptions(ctx, renewCode, "fixed", 30, 0, 0, nil, repo.CouponOptions{ApplyScope: "renew"}); err != nil {
		t.Fatal(err)
	}
	if err := coupons.Create(ctx, newCode, "fixed", 30, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			query string
			arg   any
		}{
			{`DELETE FROM coupon_usages WHERE coupon_id IN (SELECT id FROM coupons WHERE code IN ($1,$2))`, nil},
			{`DELETE FROM coupons WHERE code IN ($1,$2)`, nil},
			{`DELETE FROM invoices WHERE user_id=$1`, uid},
			{`DELETE FROM orders WHERE user_id=$1`, uid},
			{`DELETE FROM services WHERE id=$1`, svcID},
			{`DELETE FROM product_prices WHERE product_id=$1`, pid},
			{`DELETE FROM products WHERE id=$1`, pid},
			{`DELETE FROM users WHERE id=$1`, uid},
		} {
			var err error
			if q.arg != nil {
				_, err = d.ExecContext(ctx, q.query, q.arg)
			} else {
				_, err = d.ExecContext(ctx, q.query, renewCode, newCode)
			}
			if err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})

	orders := &Orders{db: d, Products: products, Coupons: coupons}

	// new 券用于续费：场景拒绝且不落单
	_, _, _, err = orders.CreateRenewOrder(ctx, uid, svcID, "monthly", newCode)
	if !errors.Is(err, repo.ErrCouponScene) {
		t.Fatalf("new 券续费应报场景错误，实得 %v", err)
	}
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE user_id=$1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("场景拒绝时不得落单")
	}

	// renew 券抵扣：100 - 30 = 70.00
	_, _, amount, err := orders.CreateRenewOrder(ctx, uid, svcID, "monthly", renewCode)
	if err != nil {
		t.Fatalf("renew 券续费下单失败: %v", err)
	}
	if amount != "70.00" {
		t.Fatalf("续费用券金额应为 70.00，实得 %s", amount)
	}
}
