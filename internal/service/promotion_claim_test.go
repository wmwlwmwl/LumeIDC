package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"lumeidc/internal/repo"
)

// claimCouponFixture 一个「进行中/已结束/已禁用」的赠券活动及其券模板与绑定。
type claimCouponFixture struct {
	db        *sql.DB
	userID    int64
	promoID   int64
	bindingID int64
	tplID     int64
}

// setupClaimCouponFixture 造一个赠券活动（绑定一张未过期的固定金额券模板），
// endsAt/enabled 由用例控制，用于验证领取的时间与启用门槛。
func setupClaimCouponFixture(t *testing.T, d *sql.DB, tag, endsAt string, enabled bool) *claimCouponFixture {
	t.Helper()
	ctx := context.Background()
	f := &claimCouponFixture{db: d}
	insert := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("夹具插入失败(%s): %v", query, err)
		}
		return id
	}
	now := time.Now().Format("150405.000000000")
	f.userID = insert(`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"claimcoupon-"+tag+"-"+now+"@example.invalid")
	f.tplID = insert(`INSERT INTO coupons(code,type,value,min_amount,usage_limit,expires_at,active)
		VALUES($1,'fixed',10,0,1,now()+interval '30 days',true) RETURNING id`,
		"TPL-"+now)
	productID := insert(`INSERT INTO products(type_id,name,stock) VALUES(NULL,$1,-1) RETURNING id`,
		"单元测试-赠券活动商品-"+tag)
	f.promoID = insert(fmt.Sprintf(
		`INSERT INTO promotions(name,type,starts_at,ends_at,enabled,limit_per_user)
		 VALUES($1,'coupon_giveaway',now()-interval '1 day',%s,$2,0) RETURNING id`, endsAt),
		"单元测试-赠券活动-"+tag, enabled)
	f.bindingID = insert(fmt.Sprintf(
		`INSERT INTO promotion_products(promotion_id,product_id,rules)
		 VALUES($1,$2,'{"coupon_id":%d}'::jsonb) RETURNING id`, f.tplID),
		f.promoID, productID)
	t.Cleanup(func() {
		ctx := context.Background()
		// 顺序照顾外键：领券记录 → 优惠券（模板与专属券）→ 活动（级联绑定）→ 商品 → 用户
		for _, q := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM promotion_coupon_claims WHERE promotion_id=$1`, []any{f.promoID}},
			{`DELETE FROM coupons WHERE id=$1 OR user_id=$2`, []any{f.tplID, f.userID}},
			{`DELETE FROM promotions WHERE id=$1`, []any{f.promoID}},
			{`DELETE FROM products WHERE id=$1`, []any{productID}},
			{`DELETE FROM users WHERE id=$1`, []any{f.userID}},
		} {
			if _, err := d.ExecContext(ctx, q.query, q.args...); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q.query, err)
			}
		}
	})
	return f
}

// 领券是活动期间的"新操作"：活动结束或被禁用后服务端必须拒绝，
// 且拒绝时不得留下领取记录或专属券（否则结束后仍能量产优惠）。
func TestClaimCouponRejectsEndedOrDisabledPromotion(t *testing.T) {
	cases := []struct {
		name    string
		endsAt  string
		enabled bool
		wantErr string
	}{
		{"已结束", `now()-interval '1 hour'`, true, "当前不在活动领取时间内"},
		{"已禁用", `now()+interval '1 day'`, false, "活动不存在或未启用"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDB(t)
			ctx := context.Background()
			f := setupClaimCouponFixture(t, d, c.name, c.endsAt, c.enabled)
			svc := NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))

			err := svc.ClaimCoupon(ctx, f.promoID, f.bindingID, f.userID)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("应拒绝领取（期望包含 %q），实得 %v", c.wantErr, err)
			}
			var claims, userCoupons int
			if err := d.QueryRowContext(ctx, `SELECT count(*) FROM promotion_coupon_claims WHERE promotion_id=$1`, f.promoID).Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRowContext(ctx, `SELECT count(*) FROM coupons WHERE user_id=$1`, f.userID).Scan(&userCoupons); err != nil {
				t.Fatal(err)
			}
			if claims != 0 || userCoupons != 0 {
				t.Fatalf("拒绝领取时不得生成领取记录或专属券，实得 claims=%d userCoupons=%d", claims, userCoupons)
			}
		})
	}
}

// 对照组：进行中的赠券活动应能成功领取且只能领一次。
// 少了这条，上面两个拒绝用例可能因夹具本身失效而假阳性。
func TestClaimCouponSucceedsWhileOngoing(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	f := setupClaimCouponFixture(t, d, "ongoing", `now()+interval '1 day'`, true)
	svc := NewPromotionService(d, repo.NewPromotions(d), repo.NewCoupons(d))

	if err := svc.ClaimCoupon(ctx, f.promoID, f.bindingID, f.userID); err != nil {
		t.Fatalf("进行中的活动应允许领取，实得错误: %v", err)
	}
	var claims, userCoupons int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM promotion_coupon_claims WHERE promotion_id=$1 AND user_id=$2`, f.promoID, f.userID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM coupons WHERE user_id=$1`, f.userID).Scan(&userCoupons); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || userCoupons != 1 {
		t.Fatalf("领取后应各有 1 条记录，实得 claims=%d userCoupons=%d", claims, userCoupons)
	}

	if err := svc.ClaimCoupon(ctx, f.promoID, f.bindingID, f.userID); err == nil || !strings.Contains(err.Error(), "您已领取过") {
		t.Fatalf("重复领取应被拒绝，实得 %v", err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM coupons WHERE user_id=$1`, f.userID).Scan(&userCoupons); err != nil {
		t.Fatal(err)
	}
	if userCoupons != 1 {
		t.Fatalf("重复领取不得再生成专属券，实得 %d", userCoupons)
	}
}
