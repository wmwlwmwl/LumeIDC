package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"lumeidc/internal/repo"
)

// promotionBindingFixture 活动绑定归属校验的测试夹具：
// 一个进行中折扣活动（活动价 1 元）在 ownProduct 上有三个绑定（不限周期 / 限 yearly / 无关商品各一），
// 外加一个已结束活动的绑定。
type promotionBindingFixture struct {
	userID          int64
	ownProductID    int64
	otherProductID  int64
	promoID         int64
	endedPromoID    int64
	ownBindingID    int64
	otherBindingID  int64
	yearlyBindingID int64
	endedBindingID  int64
	psID            int64
}

func setupPromotionBindingFixture(t *testing.T, d *sql.DB, tag string) *promotionBindingFixture {
	t.Helper()
	ctx := context.Background()
	products := repo.NewProducts(d)
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	f := &promotionBindingFixture{psID: psID}
	insert := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("夹具插入失败(%s): %v", query, err)
		}
		return id
	}
	f.userID = insert(`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"promobind-"+tag+"-"+time.Now().Format("150405.000000000")+"@example.invalid")
	typeID := insert(`INSERT INTO product_types(name) VALUES($1) RETURNING id`, "单元测试-活动绑定-"+tag)
	f.ownProductID = insert(`INSERT INTO products(type_id,name,stock,requires_identity) VALUES($1,$2,-1,false) RETURNING id`,
		typeID, "单元测试-活动绑定商品-"+tag)
	f.otherProductID = insert(`INSERT INTO products(type_id,name,stock,requires_identity) VALUES($1,$2,-1,false) RETURNING id`,
		typeID, "单元测试-活动绑定无关商品-"+tag)
	for _, pid := range []int64{f.ownProductID, f.otherProductID} {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,100,0,0)`,
			pid, psID); err != nil {
			t.Fatalf("夹具插入价格失败: %v", err)
		}
	}
	f.promoID = insert(`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		VALUES($1,'discount',now()-interval '1 day',now()+interval '1 day',true,0) RETURNING id`,
		"单元测试-活动绑定折扣-"+tag)
	f.endedPromoID = insert(`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		VALUES($1,'discount',now()-interval '2 day',now()-interval '1 day',true,0) RETURNING id`,
		"单元测试-活动绑定已结束-"+tag)
	f.ownBindingID = insert(`INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,'{"price":1}'::jsonb) RETURNING id`, f.promoID, f.ownProductID)
	f.otherBindingID = insert(`INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,'{"price":1}'::jsonb) RETURNING id`, f.promoID, f.otherProductID)
	f.yearlyBindingID = insert(`INSERT INTO promotion_products(promotion_id,product_id,cycle,rules)
		VALUES($1,$2,'yearly','{"price":1}'::jsonb) RETURNING id`, f.promoID, f.ownProductID)
	f.endedBindingID = insert(`INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,'{"price":1}'::jsonb) RETURNING id`, f.endedPromoID, f.ownProductID)
	t.Cleanup(func() {
		ctx := context.Background()
		// 顺序照顾外键：活动（级联 promotion_products）→ 账单/订单 → 价格 → 商品 → 类型 → 用户
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM invoices WHERE user_id=$1`, f.userID},
			{`DELETE FROM orders WHERE user_id=$1`, f.userID},
			{`DELETE FROM promotions WHERE id=$1`, f.promoID},
			{`DELETE FROM promotions WHERE id=$1`, f.endedPromoID},
			{`DELETE FROM product_prices WHERE product_id=$1`, f.ownProductID},
			{`DELETE FROM product_prices WHERE product_id=$1`, f.otherProductID},
			{`DELETE FROM products WHERE id=$1`, f.ownProductID},
			{`DELETE FROM products WHERE id=$1`, f.otherProductID},
			{`DELETE FROM product_types WHERE id=$1`, typeID},
			{`DELETE FROM users WHERE id=$1`, f.userID},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})
	return f
}

// 购买页指定活动绑定时，服务端必须校验绑定归属：
// 只有「该商品 + 价格组 + 周期」下生效的绑定才会被采纳；
// 属于别的商品/周期、活动已结束、promotion_id 与绑定不一致、ID 非正数的一律拒绝，不静默换活动。
func TestActivePromotionForRequestedValidatesBindingOwnership(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	f := setupPromotionBindingFixture(t, d, "owner")
	svc := NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))

	ap, err := svc.ActivePromotionForRequested(ctx, f.ownProductID, f.psID, "monthly", f.promoID, f.ownBindingID)
	if err != nil {
		t.Fatalf("指定自身生效绑定应被采纳，实得错误: %v", err)
	}
	if ap == nil || ap.PromotionProductID != f.ownBindingID || ap.Type != "discount" || ap.Price != 1 {
		t.Fatalf("采纳的活动绑定不符: %+v", ap)
	}

	mismatches := []struct {
		name                  string
		productID             int64
		promotionID           int64
		promotionProductID    int64
	}{
		{"跨商品指定绑定", f.otherProductID, f.promoID, f.ownBindingID},
		{"周期不匹配的绑定", f.ownProductID, f.promoID, f.yearlyBindingID},
		{"已结束活动的绑定", f.ownProductID, f.endedPromoID, f.endedBindingID},
		{"promotion_id与绑定不一致", f.ownProductID, f.endedPromoID, f.ownBindingID},
		{"promotion_id非正数", f.ownProductID, 0, f.ownBindingID},
		{"promotion_product_id非正数", f.ownProductID, f.promoID, 0},
	}
	for _, tc := range mismatches {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.ActivePromotionForRequested(ctx, tc.productID, f.psID, "monthly", tc.promotionID, tc.promotionProductID); !errors.Is(err, ErrPromotionBindingMismatch) {
				t.Fatalf("必须返回 ErrPromotionBindingMismatch，实得 %v", err)
			}
		})
	}
}

// 下单链路：指定生效绑定按该活动权威计价并落库；
// 指定不属于该商品的绑定必须拒绝下单；未指定时回退服务端按优先级解析。
func TestCreateOrderHonorsRequestedPromotionBinding(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	f := setupPromotionBindingFixture(t, d, "order")
	orders := &Orders{db: d, Products: repo.NewProducts(d),
		Promotion: NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))}

	orderID, _, amount, err := orders.CreateOrder(ctx, f.userID, f.ownProductID, f.psID, "monthly", map[string]string{}, "", f.promoID, f.ownBindingID)
	if err != nil {
		t.Fatalf("指定生效绑定应能按活动价下单，实得错误: %v", err)
	}
	if amount != "1.00" {
		t.Fatalf("指定绑定应按活动价 1.00 成交，实得 %s", amount)
	}
	var gotPromoID, gotPromoProductID int64
	if err := d.QueryRowContext(ctx,
		`SELECT promotion_id,promo_product_id FROM orders WHERE id=$1`, orderID).Scan(&gotPromoID, &gotPromoProductID); err != nil {
		t.Fatal(err)
	}
	if gotPromoID != f.promoID || gotPromoProductID != f.ownBindingID {
		t.Fatalf("订单应记录指定活动绑定，实得 promotion_id=%d promo_product_id=%d", gotPromoID, gotPromoProductID)
	}

	if _, _, _, err := orders.CreateOrder(ctx, f.userID, f.ownProductID, f.psID, "monthly", map[string]string{}, "", f.promoID, f.otherBindingID); !errors.Is(err, ErrPromotionBindingMismatch) {
		t.Fatalf("指定别的商品的绑定必须拒绝下单，实得 %v", err)
	}
	var orderCount int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE user_id=$1`, f.userID).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	if orderCount != 1 {
		t.Fatalf("被拒绝的下单不应落单，实有订单 %d 笔", orderCount)
	}

	_, _, fallbackAmount, err := orders.CreateOrder(ctx, f.userID, f.ownProductID, f.psID, "monthly", map[string]string{}, "", 0, 0)
	if err != nil {
		t.Fatalf("未指定绑定时应回退服务端解析，实得错误: %v", err)
	}
	if fallbackAmount != "1.00" {
		t.Fatalf("回退解析应命中同一活动价 1.00，实得 %s", fallbackAmount)
	}
}
