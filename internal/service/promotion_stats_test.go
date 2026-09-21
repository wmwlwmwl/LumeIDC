package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"lumeidc/internal/repo"
)

// promotionStatsFixture 一笔「未支付的活动订单 + 未支付账单」及其上游活动数据。
type promotionStatsFixture struct {
	db        *sql.DB
	userID    int64
	productID int64
	promoID   int64
	bindingID int64
	orderID   int64
	invoiceNo string
}

// setupPromotionStatsFixture 造一笔绑定进行中折扣活动的未支付订单（实付 1.00）与未支付账单，
// 用户余额充足，供余额/线上两条核销路径共用。
func setupPromotionStatsFixture(t *testing.T, d *sql.DB, tag string) *promotionStatsFixture {
	t.Helper()
	ctx := context.Background()
	f := &promotionStatsFixture{db: d}
	var psID int64
	if err := d.QueryRowContext(ctx, `SELECT min(id) FROM pricesets`).Scan(&psID); err != nil {
		t.Skipf("库中暂无价格组，跳过: %v", err)
	}
	insert := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("夹具插入失败(%s): %v", query, err)
		}
		return id
	}
	f.userID = insert(`INSERT INTO users(email,password_hash,balance) VALUES($1,'x','10.00') RETURNING id`,
		"promostats-"+tag+"-"+time.Now().Format("150405.000000000")+"@example.invalid")
	f.productID = insert(`INSERT INTO products(type_id,name,stock) VALUES(NULL,$1,-1) RETURNING id`,
		"单元测试-活动统计商品-"+tag)
	f.promoID = insert(`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		VALUES($1,'discount',now()-interval '1 day',now()+interval '1 day',true,0) RETURNING id`,
		"单元测试-活动统计折扣-"+tag)
	f.bindingID = insert(`INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,'{"price":1}'::jsonb) RETURNING id`, f.promoID, f.productID)
	f.orderID = insert(`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,promotion_id,promo_product_id,promo_type)
		VALUES($1,$2,$3,'monthly','1.00',0,$4,$5,'discount') RETURNING id`,
		f.userID, f.productID, psID, f.promoID, f.bindingID)
	f.invoiceNo = "PROMOSTATS-" + time.Now().Format("150405.000000000")
	if _, err := d.ExecContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,status) VALUES($1,$2,$3,'1.00',0)`,
		f.invoiceNo, f.userID, f.orderID); err != nil {
		t.Fatalf("夹具插入账单失败: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// 顺序照顾外键：账单 → 服务（级联履约任务）→ 订单 → 活动（级联绑定）→ 商品 → 账务 → 用户
		for _, q := range []struct {
			query string
			arg   int64
		}{
			{`DELETE FROM invoices WHERE order_id=$1`, f.orderID},
			{`DELETE FROM services WHERE order_id=$1`, f.orderID},
			{`DELETE FROM orders WHERE id=$1`, f.orderID},
			{`DELETE FROM promotions WHERE id=$1`, f.promoID},
			{`DELETE FROM products WHERE id=$1`, f.productID},
			{`DELETE FROM balance_logs WHERE user_id=$1`, f.userID},
			{`DELETE FROM users WHERE id=$1`, f.userID},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})
	return f
}

// stats 读取该活动的累计统计（下单量、成交金额）。
func (f *promotionStatsFixture) stats(t *testing.T) (orders int, paid float64) {
	t.Helper()
	if err := f.db.QueryRow(`SELECT orders,paid_amount::float8 FROM promotion_stats WHERE promotion_id=$1`, f.promoID).
		Scan(&orders, &paid); err != nil {
		t.Fatalf("查询活动统计失败: %v", err)
	}
	return orders, paid
}

func promotionStatsPayment(d *sql.DB) *Payment {
	return &Payment{db: d, Jobs: repo.NewFulfillmentJobs(d), Balance: repo.NewBalance(d),
		Promotion: NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))}
}

// 余额支付核销活动订单也必须累加活动统计（订单数 +1、成交金额 += 实付），
// 且与线上支付一致地落在同一事务内随 commit 生效；重复核销被幂等挡下，统计不重复累加。
func TestMarkPaidByBalanceAccumulatesPromotionStats(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	f := setupPromotionStatsFixture(t, d, "balancestats")
	p := promotionStatsPayment(d)

	if err := p.MarkPaidByBalance(ctx, f.invoiceNo, f.userID); err != nil {
		t.Fatalf("余额支付核销活动订单应成功: %v", err)
	}
	if orders, paid := f.stats(t); orders != 1 || paid != 1 {
		t.Fatalf("核销后应 orders=1、paid_amount=1.00，实得 orders=%d、paid_amount=%v", orders, paid)
	}
	if err := p.MarkPaidByBalance(ctx, f.invoiceNo, f.userID); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("重复核销应返回 ErrAlreadyPaid，实得 %v", err)
	}
	if orders, paid := f.stats(t); orders != 1 || paid != 1 {
		t.Fatalf("重复核销后统计不应重复累加，实得 orders=%d、paid_amount=%v", orders, paid)
	}
}

// 线上支付（MarkPaid）路径同样只累加一次：回归保护，
// 防止统计被挪出事务（commit 失败也记了账）或被重复计数。
func TestMarkPaidAccumulatesPromotionStatsOnce(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	f := setupPromotionStatsFixture(t, d, "markpaid")
	p := promotionStatsPayment(d)

	if err := p.MarkPaid(ctx, f.invoiceNo, "BALANCE-"+f.invoiceNo, "balance"); err != nil {
		t.Fatalf("核销活动订单应成功: %v", err)
	}
	if orders, paid := f.stats(t); orders != 1 || paid != 1 {
		t.Fatalf("核销后应 orders=1、paid_amount=1.00，实得 orders=%d、paid_amount=%v", orders, paid)
	}
	if err := p.MarkPaid(ctx, f.invoiceNo, "BALANCE-"+f.invoiceNo, "balance"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("重复核销应返回 ErrAlreadyPaid，实得 %v", err)
	}
	if orders, paid := f.stats(t); orders != 1 || paid != 1 {
		t.Fatalf("重复核销后统计不应重复累加，实得 orders=%d、paid_amount=%v", orders, paid)
	}
}
