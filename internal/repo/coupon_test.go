package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// 优惠码场景化测试夹具：真实库（无 TEST_DATABASE_DSN 时跳过）。
func setupCouponTest(t *testing.T) (*sql.DB, *Coupons, context.Context) {
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
	return d, NewCoupons(d), context.Background()
}

func newCouponUser(t *testing.T, d *sql.DB, ctx context.Context) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		fmt.Sprintf("coupon-test-%d@example.invalid", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	})
	return id
}

// validateInTx 在独立事务内校验（FOR UPDATE 必须在事务内），校验完即回滚不落库。
func validateInTx(t *testing.T, d *sql.DB, c *Coupons, ctx context.Context, code string, userID int64, amount, scene string) error {
	t.Helper()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, _, err = c.Validate(ctx, tx, code, userID, amount, scene)
	return err
}

// 适用范围：renew 券只能续费用，new 券只能新购用，both 均可。
func TestCouponApplyScope(t *testing.T) {
	d, c, ctx := setupCouponTest(t)
	uid := newCouponUser(t, d, ctx)
	ts := time.Now().UnixNano()

	renewCode := fmt.Sprintf("TRENEW%d", ts%1000000000)
	if err := c.CreateWithOptions(ctx, renewCode, "fixed", 5, 0, 0, nil, CouponOptions{ApplyScope: "renew"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM coupons WHERE code=$1`, renewCode) })

	if err := validateInTx(t, d, c, ctx, renewCode, uid, "100.00", "new"); !errors.Is(err, ErrCouponScene) {
		t.Fatalf("renew 券在新购场景应拒绝，实得 %v", err)
	}
	if err := validateInTx(t, d, c, ctx, renewCode, uid, "100.00", "renew"); err != nil {
		t.Fatalf("renew 券在续费场景应通过，实得 %v", err)
	}

	bothCode := fmt.Sprintf("TBOTH%d", ts%1000000000)
	if err := c.CreateWithOptions(ctx, bothCode, "fixed", 5, 0, 0, nil, CouponOptions{ApplyScope: "both"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM coupons WHERE code=$1`, bothCode) })
	if err := validateInTx(t, d, c, ctx, bothCode, uid, "100.00", "new"); err != nil {
		t.Fatalf("both 券新购应通过，实得 %v", err)
	}
	if err := validateInTx(t, d, c, ctx, bothCode, uid, "100.00", "renew"); err != nil {
		t.Fatalf("both 券续费应通过，实得 %v", err)
	}

	newCode := fmt.Sprintf("TNEW%d", ts%1000000000)
	if err := c.Create(ctx, newCode, "fixed", 5, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM coupons WHERE code=$1`, newCode) })
	if err := validateInTx(t, d, c, ctx, newCode, uid, "100.00", "renew"); !errors.Is(err, ErrCouponScene) {
		t.Fatalf("默认 new 券在续费场景应拒绝，实得 %v", err)
	}
}

// 循环期数：recurring=N 时同一用户总共可用 N+1 次。
func TestCouponRecurringLimit(t *testing.T) {
	d, c, ctx := setupCouponTest(t)
	uid := newCouponUser(t, d, ctx)
	code := fmt.Sprintf("TLOOP%d", time.Now().UnixNano()%1000000000)
	if err := c.CreateWithOptions(ctx, code, "fixed", 5, 0, 0, nil, CouponOptions{ApplyScope: "both", Recurring: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM coupons WHERE code=$1`, code)
	})

	// 第 1 次：新购场景通过并落使用记录（真实提交，占用次数）
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cid, _, err := c.Validate(ctx, tx, code, uid, "100.00", "new")
	if err != nil {
		tx.Rollback()
		t.Fatalf("第 1 次用券应通过，实得 %v", err)
	}
	if err := c.Use(ctx, tx, cid, uid, 0, "5.00"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.ExecContext(ctx, `DELETE FROM coupon_usages WHERE coupon_id=$1`, cid)
	})

	// 第 2 次（recurring=1，总共 2 次）：续费场景仍可用
	if err := validateInTx(t, d, c, ctx, code, uid, "100.00", "renew"); err != nil {
		t.Fatalf("recurring=1 第 2 次应通过，实得 %v", err)
	}

	// 再补一次使用记录，第 3 次应被拒
	tx2, _ := d.BeginTx(ctx, nil)
	if err := c.Use(ctx, tx2, cid, uid, 0, "5.00"); err != nil {
		tx2.Rollback()
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := validateInTx(t, d, c, ctx, code, uid, "100.00", "renew"); !errors.Is(err, ErrCouponUsed) {
		t.Fatalf("超出 1+recurring 次应拒绝，实得 %v", err)
	}
}

// 需求商品：须持有激活（status=1）的指定产品服务才可用券。
func TestCouponNeedProduct(t *testing.T) {
	d, c, ctx := setupCouponTest(t)
	uid := newCouponUser(t, d, ctx)
	var pid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(name,stock) VALUES($1,-1) RETURNING id`,
		fmt.Sprintf("coupon-need-%d", time.Now().UnixNano())).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, pid) })

	code := fmt.Sprintf("TNEED%d", time.Now().UnixNano()%1000000000)
	if err := c.CreateWithOptions(ctx, code, "fixed", 5, 0, 0, nil, CouponOptions{NeedProductIDs: []int64{pid}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM coupons WHERE code=$1`, code) })

	if err := validateInTx(t, d, c, ctx, code, uid, "100.00", "new"); !errors.Is(err, ErrCouponNeedProduct) {
		t.Fatalf("无需求商品服务应拒绝，实得 %v", err)
	}

	// 停机（status=2）不算持有
	var svcID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status) VALUES($1,$2,2) RETURNING id`, uid, pid).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.ExecContext(ctx, `DELETE FROM services WHERE id=$1`, svcID) })
	if err := validateInTx(t, d, c, ctx, code, uid, "100.00", "new"); !errors.Is(err, ErrCouponNeedProduct) {
		t.Fatalf("停机服务不算持有，应拒绝，实得 %v", err)
	}

	// 激活（status=1）后放行
	if _, err := d.ExecContext(ctx, `UPDATE services SET status=1 WHERE id=$1`, svcID); err != nil {
		t.Fatal(err)
	}
	if err := validateInTx(t, d, c, ctx, code, uid, "100.00", "new"); err != nil {
		t.Fatalf("持有激活服务应通过，实得 %v", err)
	}
}
