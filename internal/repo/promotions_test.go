package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// 限购校验的语句必须能执行：PostgreSQL 禁止聚合函数与行锁同用，
// 旧实现写成 `count(*) ... FOR UPDATE` 会直接报错，令凡配置了「每人限购」的活动一单都下不了。
// 本用例固定住这一点（无超限分支覆盖，超限时返回 ErrPromotionLimitExceeded）。
func TestPromotionStatusAtEndIsEnded(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	promotion := Promotion{
		StartsAt: now.Add(-time.Hour),
		EndsAt:   now,
	}
	if got := promotion.statusAt(now); got != "ended" {
		t.Fatalf("结束时间等于当前时间时应为 ended，实得 %q", got)
	}
}

func TestPromotionCreateTxRollsBackWithTransaction(t *testing.T) {
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
	p := NewPromotions(d)
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	promotion := &Promotion{
		Name:     "transaction-test",
		Type:     "discount",
		StartsAt: time.Now().Add(-time.Minute),
		EndsAt:   time.Now().Add(time.Minute),
		Enabled:  true,
	}
	id, err := p.CreateTx(ctx, tx, promotion)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM promotions WHERE id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("事务内应能读取新活动，count=%d", count)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM promotions WHERE id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("回滚后活动不应残留，count=%d", count)
	}
}

func TestPromotionProductCouponTemplateUsesSelectedBinding(t *testing.T) {
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
	p := NewPromotions(d)
	var promotionID, productID, firstProductID, secondProductID int64
	if err := d.QueryRowContext(ctx, `
		INSERT INTO promotions(name,type,starts_at,ends_at)
		VALUES('coupon-binding-test','coupon_giveaway',now() - interval '1 minute',now() + interval '1 minute')
		RETURNING id`).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	defer d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, promotionID)
	// 绑定商品必须真实存在，避免依赖库中恰好有 id=1 的商品。
	if err := d.QueryRowContext(ctx, `INSERT INTO products(name,stock) VALUES('coupon-binding-test-product',-1) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	defer d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)

	if err := d.QueryRowContext(ctx, `
		INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,jsonb_build_object('coupon_id',101))
		RETURNING id`, promotionID, productID).Scan(&firstProductID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `
		INSERT INTO promotion_products(promotion_id,product_id,rules)
		VALUES($1,$2,jsonb_build_object('coupon_id',202))
		RETURNING id`, promotionID, productID).Scan(&secondProductID); err != nil {
		t.Fatal(err)
	}

	couponID, err := p.CouponTemplateID(ctx, promotionID, secondProductID)
	if err != nil {
		t.Fatal(err)
	}
	if couponID != 202 {
		t.Fatalf("选择第二个活动商品应使用其绑定模板，得到 %d", couponID)
	}
	_ = firstProductID
}

func TestIncrementClaimedTxIncrementsPromotionStats(t *testing.T) {
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
	p := NewPromotions(d)
	var promotionID int64
	if err := d.QueryRowContext(ctx, `
		INSERT INTO promotions(name,type,starts_at,ends_at)
		VALUES('claimed-stats-test','coupon_giveaway',now() - interval '1 minute',now() + interval '1 minute')
		RETURNING id`).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	defer d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, promotionID)

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.IncrementClaimedTx(ctx, tx, promotionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var claimed int
	if err := d.QueryRowContext(ctx, `SELECT claimed FROM promotion_stats WHERE promotion_id=$1`, promotionID).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed != 1 {
		t.Fatalf("首次成功领券应将 claimed 增加到 1，得到 %d", claimed)
	}
}

func TestTryReserveQuotaConcurrentSingleStock(t *testing.T) {
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
	var productID int64
	if err := d.QueryRowContext(ctx, `
		INSERT INTO products(name,stock) VALUES('quota-concurrency-test',-1)
		RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	var promotionProductID int64
	if err := d.QueryRowContext(ctx, `
		INSERT INTO promotion_products(promotion_id,product_id,rules)
		SELECT id,$1,'{}'::jsonb FROM promotions LIMIT 1
		RETURNING id`, productID).Scan(&promotionProductID); err != nil {
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
		t.Skip("库中暂无活动主表，跳过")
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO promotion_quota(promotion_product_id,total,sold)
		VALUES($1,1,0)`, promotionProductID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM promotion_products WHERE id=$1`, promotionProductID)
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
	})

	p := NewPromotions(d)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				results <- err
				return
			}
			err = p.TryReserveQuota(ctx, tx, promotionProductID)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			results <- err
		}()
	}
	var success int
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, ErrPromotionSoldOut) {
			t.Fatalf("并发抢购出现非预期错误: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("单库存并发只能成功一次，成功次数=%d", success)
	}
}

func TestTryReserveQuotaRollbackDoesNotConsumeStock(t *testing.T) {
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
	var productID, promotionProductID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO products(name,stock) VALUES('quota-rollback-test',-1) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO promotion_products(promotion_id,product_id,rules) SELECT id,$1,'{}'::jsonb FROM promotions LIMIT 1 RETURNING id`, productID).Scan(&promotionProductID); err != nil {
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
		t.Skip("库中暂无活动主表，跳过")
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO promotion_quota(promotion_product_id,total,sold) VALUES($1,1,0)`, promotionProductID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM promotion_products WHERE id=$1`, promotionProductID)
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
	})
	p := NewPromotions(d)
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 0 {
		t.Fatalf("事务回滚后库存占用应恢复为 0，实得 %d", sold)
	}
}

func TestIsNewUserTxReturnsFalseAfterOrder(t *testing.T) {
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
	var userID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`, "new-user-test-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM orders WHERE user_id=$1`, userID)
		d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	p := NewPromotions(d)
	isNew, err := p.IsNewUser(ctx, userID)
	if err != nil || !isNew {
		t.Fatalf("无历史订单用户应为新客，isNew=%v err=%v", isNew, err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount)
		 VALUES($1,(SELECT min(id) FROM products),(SELECT min(id) FROM pricesets),'monthly',1)`,
		userID); err != nil {
		t.Fatal(err)
	}
	isNew, err = p.IsNewUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Fatal("已有历史订单用户不应继续判定为新客")
	}
}

func TestIsNewUserTxSeesOrderInSameTransaction(t *testing.T) {
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
	var userID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`, "new-user-tx-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM orders WHERE user_id=$1`, userID)
		d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount)
		 VALUES($1,(SELECT min(id) FROM products),(SELECT min(id) FROM pricesets),'monthly',1)`,
		userID); err != nil {
		t.Fatal(err)
	}
	isNew, err := NewPromotions(d).IsNewUserTx(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Fatal("同一事务内已有订单时不应继续享受新客活动")
	}
}

func TestCheckLimitPerUserRollbackDoesNotConsumeQuota(t *testing.T) {
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

	var userID, productID, promotionID, promotionProductID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`, "limit-rollback-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO products(name,stock) VALUES('limit-rollback-test',-1) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO promotions(name,type,starts_at,ends_at,limit_per_user) VALUES('limit-rollback-test','flash_sale',now() - interval '1 minute',now() + interval '1 minute',1) RETURNING id`).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO promotion_products(promotion_id,product_id,rules) VALUES($1,$2,'{}'::jsonb) RETURNING id`, promotionID, productID).Scan(&promotionProductID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO promotion_quota(promotion_product_id,total,sold) VALUES($1,1,0)`, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,promotion_id)
		 VALUES($1,$2,(SELECT min(id) FROM pricesets),'monthly',1,$3)`,
		userID, productID, promotionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM orders WHERE user_id=$1`, userID)
		d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, promotionID)
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
		d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPromotions(d)
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckLimitPerUser(ctx, tx, promotionID, userID, 1); !errors.Is(err, ErrPromotionLimitExceeded) {
		t.Fatalf("第二单应命中限购错误，实得 %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 0 {
		t.Fatalf("限购失败回滚后库存不应增加，实得 %d", sold)
	}
}

func TestSaveProductsRemovesQuotaWhenStockBecomesUnlimited(t *testing.T) {
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

	var promotionID, productID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO promotions(name,type,starts_at,ends_at) VALUES('quota-update-test','flash_sale',now() - interval '1 minute',now() + interval '1 minute') RETURNING id`).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO products(name,stock) VALUES('quota-update-test',-1) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, promotionID)
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
	})

	p := NewPromotions(d)
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SaveProducts(ctx, tx, promotionID, []PromotionProduct{{ProductID: productID, Rules: []byte(`{"stock":2}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var promotionProductID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM promotion_products WHERE promotion_id=$1`, promotionID).Scan(&promotionProductID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE promotion_quota SET sold=1 WHERE promotion_product_id=$1`, promotionProductID); err != nil {
		t.Fatal(err)
	}

	tx, err = d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SaveProducts(ctx, tx, promotionID, []PromotionProduct{{ProductID: productID, Rules: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var quotaCount int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM promotion_quota q JOIN promotion_products pp ON pp.id=q.promotion_product_id WHERE pp.promotion_id=$1`, promotionID).Scan(&quotaCount); err != nil {
		t.Fatal(err)
	}
	if quotaCount != 0 {
		t.Fatalf("改为不限量后不应残留 quota，记录数=%d", quotaCount)
	}
}

// setupQuotaFixture 为抢购名额测试创建独立的商品、活动和名额记录。
// total<0 表示不创建 quota 行（不限量）。
func setupQuotaFixture(t *testing.T, name string, total, sold int) (*sql.DB, *Promotions, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	var productID, promotionID, promotionProductID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO products(name,stock) VALUES($1,-1) RETURNING id`, name).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO promotions(name,type,starts_at,ends_at) VALUES($1,'flash_sale',now()-interval '1 minute',now()+interval '1 minute') RETURNING id`,
		name).Scan(&promotionID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO promotion_products(promotion_id,product_id,rules) VALUES($1,$2,'{}'::jsonb) RETURNING id`,
		promotionID, productID).Scan(&promotionProductID); err != nil {
		t.Fatal(err)
	}
	if total >= 0 {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO promotion_quota(promotion_product_id,total,sold) VALUES($1,$2,$3)`,
			promotionProductID, total, sold); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, promotionID)
		d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, productID)
	})
	return d, NewPromotions(d), promotionProductID
}

// 已售罄的名额应直接拒绝，而不是继续递增 sold。
func TestTryReserveQuotaRejectsWhenSoldOut(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-soldout-test", 1, 1)
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); !errors.Is(err, ErrPromotionSoldOut) {
		t.Fatalf("已售罄应返回 ErrPromotionSoldOut，实得 %v", err)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 1 {
		t.Fatalf("售罄被拒后 sold 不应变化，实得 %d", sold)
	}
}

// 无 quota 行表示不限量，应直接放行。
func TestTryReserveQuotaAllowsUnlimitedWithoutRow(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-unlimited-test", -1, 0)
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatalf("不限量应放行，实得 %v", err)
	}
}

// 同一事务内第二次占用：事务能看到自己刚写入的 sold，应立即售罄。
func TestTryReserveQuotaTwiceInSameTxSecondFails(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-twice-test", 1, 0)
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); !errors.Is(err, ErrPromotionSoldOut) {
		t.Fatalf("同一事务内第二次占用应售罄，实得 %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 0 {
		t.Fatalf("回滚后 sold 应为 0，实得 %d", sold)
	}
}

// 8 个并发事务抢 3 个名额：恰好 3 个成功且最终 sold=3，不允许超卖或少卖。
func TestTryReserveQuotaConcurrentMultiStockNoOversell(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-multi-test", 3, 0)
	ctx := context.Background()
	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				results <- err
				return
			}
			err = p.TryReserveQuota(ctx, tx, promotionProductID)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			results <- err
		}()
	}
	var success int
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, ErrPromotionSoldOut) {
			t.Fatalf("并发抢购出现非预期错误: %v", err)
		}
	}
	if success != 3 {
		t.Fatalf("3 个名额应恰好成功 3 次，成功次数=%d", success)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 3 {
		t.Fatalf("并发抢购后 sold 应等于 3，实得 %d", sold)
	}
}

// 竞争事务回滚后必须释放名额：等待中的事务应能成功占到该名额。
func TestTryReserveQuotaCompetitorRollbackFreesSlot(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-competitor-test", 1, 0)
	ctx := context.Background()
	tx1, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx1, promotionProductID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx2, err := d.BeginTx(ctx, nil)
		if err != nil {
			done <- err
			return
		}
		if err := p.TryReserveQuota(ctx, tx2, promotionProductID); err != nil {
			_ = tx2.Rollback()
			done <- err
			return
		}
		done <- tx2.Commit()
	}()
	// 等待第二个事务进入行锁等待，再回滚第一个事务，模拟真实买家下单失败回滚。
	time.Sleep(300 * time.Millisecond)
	if err := tx1.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("竞争事务回滚后应能成功占到位，实得 %v", err)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 1 {
		t.Fatalf("竞争事务回滚后 sold 应为 1，实得 %d", sold)
	}
}

// 名额占用与订单写入同事务：任何一步失败回滚，两者都不残留。
func TestReserveQuotaAndOrderInsertRollbackTogether(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-atomic-test", 1, 0)
	ctx := context.Background()
	var userID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"quota-atomic-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM orders WHERE user_id=$1`, userID)
		d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,promotion_id,promo_product_id)
		 VALUES($1,(SELECT product_id FROM promotion_products WHERE id=$2),(SELECT min(id) FROM pricesets),'monthly',1,(SELECT promotion_id FROM promotion_products WHERE id=$2),$2)`,
		userID, promotionProductID); err != nil {
		t.Fatal(err)
	}
	// 模拟订单创建后续步骤失败，整体回滚。
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var sold, orderCount int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE user_id=$1`, userID).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	if sold != 0 || orderCount != 0 {
		t.Fatalf("回滚后名额与订单都不应残留，sold=%d orderCount=%d", sold, orderCount)
	}
}

// 过期释放后名额可被再次占用。
func TestReleaseQuotaByOrdersThenReserveAgain(t *testing.T) {
	d, p, promotionProductID := setupQuotaFixture(t, "quota-release-test", 1, 0)
	ctx := context.Background()
	var userID int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"quota-release-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM orders WHERE user_id=$1`, userID)
		d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	// 首个买家占用名额并留下订单。
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	var orderID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,promotion_id,promo_product_id)
		 VALUES($1,(SELECT product_id FROM promotion_products WHERE id=$2),(SELECT min(id) FROM pricesets),'monthly',1,(SELECT promotion_id FROM promotion_products WHERE id=$2),$2) RETURNING id`,
		userID, promotionProductID).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// 订单未支付过期后释放名额。
	tx, err = d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ReleaseQuotaByOrders(ctx, tx, []int64{orderID}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sold int
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 0 {
		t.Fatalf("释放后 sold 应为 0，实得 %d", sold)
	}

	// 新买家可再次占用释放出的名额。
	tx, err = d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TryReserveQuota(ctx, tx, promotionProductID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT sold FROM promotion_quota WHERE promotion_product_id=$1`, promotionProductID).Scan(&sold); err != nil {
		t.Fatal(err)
	}
	if sold != 1 {
		t.Fatalf("再次占用后 sold 应为 1，实得 %d", sold)
	}
}

func TestCheckLimitPerUserRuns(t *testing.T) {
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
	p := NewPromotions(d)

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// 不存在的活动/用户 → 计数为 0，未超限应放行。
	if err := p.CheckLimitPerUser(ctx, tx, 0, 0, 1); err != nil {
		t.Fatalf("未超限应放行，实得 %v", err)
	}
	// limit<=0 表示不限购，直接放行且不查库。
	if err := p.CheckLimitPerUser(ctx, tx, 0, 0, 0); err != nil {
		t.Fatalf("未配置限购应放行，实得 %v", err)
	}
}
