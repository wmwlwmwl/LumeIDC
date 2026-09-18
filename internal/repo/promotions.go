package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Promotions 营销活动数据访问。
type Promotions struct{ db *sql.DB }

// Promotion 活动主表。
type Promotion struct {
	ID           int64
	Name         string
	Description  string
	Type         string // discount | full_reduction | new_user | flash_sale | coupon_giveaway | bogo | group_buy
	Banner       string
	Notice       string
	RulesText    string
	StartsAt     time.Time
	EndsAt       time.Time
	Enabled      bool
	LimitPerUser int
	CreatedAt    time.Time
}

// PromotionProduct 活动-商品关联。
type PromotionProduct struct {
	ID          int64
	PromotionID int64
	ProductID   int64
	PricesetID  sql.NullInt64
	Cycle       sql.NullString
	Rules       json.RawMessage // 各类型规则参数
}

// ActivePromotion 商品命中的生效活动（含规则参数与名额）。
type ActivePromotion struct {
	Promotion  Promotion
	Product    PromotionProduct
	QuotaTotal int
	QuotaSold  int
}

// Status 活动状态：upcoming / ongoing / ended。
func (p *Promotion) Status() string {
	now := time.Now()
	if now.Before(p.StartsAt) {
		return "upcoming"
	}
	if now.After(p.EndsAt) {
		return "ended"
	}
	return "ongoing"
}

// ActivePromotionFor 查询商品当前生效的活动（按优先级取一个）。
// pricesetID / cycle 为 NULL 表示不限制；返回 nil 表示无活动。
func (p *Promotions) ActivePromotionFor(ctx context.Context, productID, pricesetID int64, cycle string) (*ActivePromotion, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT pr.id, pr.name, pr.type, pr.starts_at, pr.ends_at, pr.enabled, pr.limit_per_user,
		        pp.id, pp.product_id, pp.priceset_id, pp.cycle, pp.rules
		   FROM promotion_products pp
		   JOIN promotions pr ON pr.id=pp.promotion_id
		  WHERE pp.product_id=$1
		    AND pr.enabled=true
		    AND now() BETWEEN pr.starts_at AND pr.ends_at
		    AND (pp.priceset_id IS NULL OR pp.priceset_id=$2)
		    AND (pp.cycle IS NULL OR pp.cycle=$3)
		  ORDER BY CASE pr.type
		    WHEN 'discount' THEN 1
		    WHEN 'flash_sale' THEN 2
		    WHEN 'full_reduction' THEN 3
		    WHEN 'new_user' THEN 4
		    WHEN 'coupon_giveaway' THEN 5
		    ELSE 99 END, pr.id DESC
		  LIMIT 1`,
		productID, pricesetID, cycle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var ap ActivePromotion
	var pr Promotion
	var pp PromotionProduct
	if err := rows.Scan(
		&pr.ID, &pr.Name, &pr.Type, &pr.StartsAt, &pr.EndsAt, &pr.Enabled, &pr.LimitPerUser,
		&pp.ID, &pp.ProductID, &pp.PricesetID, &pp.Cycle, &pp.Rules,
	); err != nil {
		return nil, err
	}
	ap.Promotion = pr
	ap.Product = pp
	// 读取名额（限量抢购类型）
	var total, sold int
	if err := p.db.QueryRowContext(ctx,
		`SELECT coalesce(total,-1), coalesce(sold,0) FROM promotion_quota WHERE promotion_product_id=$1`,
		pp.ID).Scan(&total, &sold); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	ap.QuotaTotal = total
	ap.QuotaSold = sold
	return &ap, nil
}

// Get 按 ID 获取活动。
func (p *Promotions) Get(ctx context.Context, id int64) (*Promotion, error) {
	var pr Promotion
	err := p.db.QueryRowContext(ctx,
		`SELECT id,name,description,type,banner,notice,rules_text,starts_at,ends_at,enabled,limit_per_user,created_at
		   FROM promotions WHERE id=$1`, id).
		Scan(&pr.ID, &pr.Name, &pr.Description, &pr.Type, &pr.Banner, &pr.Notice, &pr.RulesText,
			&pr.StartsAt, &pr.EndsAt, &pr.Enabled, &pr.LimitPerUser, &pr.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// List 活动列表。
func (p *Promotions) List(ctx context.Context) ([]Promotion, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id,name,description,type,banner,notice,rules_text,starts_at,ends_at,enabled,limit_per_user,created_at
		   FROM promotions ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		var pr Promotion
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.Description, &pr.Type, &pr.Banner, &pr.Notice, &pr.RulesText,
			&pr.StartsAt, &pr.EndsAt, &pr.Enabled, &pr.LimitPerUser, &pr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// Create 创建活动，返回新 ID。
func (p *Promotions) Create(ctx context.Context, pr *Promotion) (int64, error) {
	var id int64
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO promotions(name,description,type,banner,notice,rules_text,starts_at,ends_at,enabled,limit_per_user)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		pr.Name, pr.Description, pr.Type, pr.Banner, pr.Notice, pr.RulesText,
		pr.StartsAt, pr.EndsAt, pr.Enabled, pr.LimitPerUser).Scan(&id)
	return id, err
}

// Update 更新活动。
func (p *Promotions) Update(ctx context.Context, pr *Promotion) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE promotions SET name=$1,description=$2,type=$3,banner=$4,notice=$5,rules_text=$6,
		        starts_at=$7,ends_at=$8,enabled=$9,limit_per_user=$10 WHERE id=$11`,
		pr.Name, pr.Description, pr.Type, pr.Banner, pr.Notice, pr.RulesText,
		pr.StartsAt, pr.EndsAt, pr.Enabled, pr.LimitPerUser, pr.ID)
	return err
}

// Delete 删除活动（级联删除关联）。
func (p *Promotions) Delete(ctx context.Context, id int64) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, id)
	return err
}

// Toggle 启用/停用。
func (p *Promotions) Toggle(ctx context.Context, id int64, enabled bool) error {
	_, err := p.db.ExecContext(ctx, `UPDATE promotions SET enabled=$1 WHERE id=$2`, enabled, id)
	return err
}

// ListProducts 活动绑定的商品列表。
func (p *Promotions) ListProducts(ctx context.Context, promotionID int64) ([]PromotionProduct, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id,promotion_id,product_id,priceset_id,cycle,rules
		   FROM promotion_products WHERE promotion_id=$1 ORDER BY id`, promotionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromotionProduct
	for rows.Next() {
		var pp PromotionProduct
		if err := rows.Scan(&pp.ID, &pp.PromotionID, &pp.ProductID, &pp.PricesetID, &pp.Cycle, &pp.Rules); err != nil {
			return nil, err
		}
		out = append(out, pp)
	}
	return out, rows.Err()
}

// SaveProducts 重设活动绑定商品（先删后插，在调用方事务内执行）。
func (p *Promotions) SaveProducts(ctx context.Context, tx *sql.Tx, promotionID int64, products []PromotionProduct) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM promotion_products WHERE promotion_id=$1`, promotionID); err != nil {
		return err
	}
	for _, pp := range products {
		var pid int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO promotion_products(promotion_id,product_id,priceset_id,cycle,rules)
			 VALUES($1,$2,$3,$4,$5) RETURNING id`,
			promotionID, pp.ProductID, pp.PricesetID, pp.Cycle, pp.Rules).Scan(&pid); err != nil {
			return err
		}
		// 限量抢购类型初始化名额
		if pp.Rules != nil {
			var rules map[string]any
			if json.Unmarshal(pp.Rules, &rules) == nil {
				if stock, ok := rules["stock"].(float64); ok && stock > 0 {
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO promotion_quota(promotion_product_id,total,sold) VALUES($1,$2,0)
						 ON CONFLICT(promotion_product_id) DO UPDATE SET total=EXCLUDED.total`,
						pid, int(stock)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// TryReserveQuota 尝试锁定限量名额（事务内 FOR UPDATE，sold < total 时 sold+1）。
// 不限量(total=-1)直接成功。售罄返回 ErrPromotionSoldOut。
func (p *Promotions) TryReserveQuota(ctx context.Context, tx *sql.Tx, promotionProductID int64) error {
	var total, sold int
	err := tx.QueryRowContext(ctx,
		`SELECT total,sold FROM promotion_quota WHERE promotion_product_id=$1 FOR UPDATE`,
		promotionProductID).Scan(&total, &sold)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // 无名额记录=不限量
	}
	if err != nil {
		return err
	}
	if total >= 0 && sold >= total {
		return ErrPromotionSoldOut
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE promotion_quota SET sold=sold+1,updated_at=now() WHERE promotion_product_id=$1`,
		promotionProductID)
	return err
}

// ReleaseQuotaByOrders 释放这批订单占用的限量名额（账单过期未支付时调用）。
// 调用方只应传入「本轮刚由未支付变为过期」的订单，故本方法可安全重复调用：
// 已支付账单的订单不释放，且不在本批订单里的历史数据不会被重复扣减。
func (p *Promotions) ReleaseQuotaByOrders(ctx context.Context, tx *sql.Tx, orderIDs []int64) error {
	if len(orderIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE promotion_quota q SET sold = GREATEST(q.sold - s.cnt, 0), updated_at=now()
		 FROM (
		    SELECT o.promo_product_id AS ppid, count(*) AS cnt
		      FROM orders o
		     WHERE o.id = ANY($1) AND o.promo_product_id IS NOT NULL
		       AND NOT EXISTS (SELECT 1 FROM invoices i2 WHERE i2.order_id=o.id AND i2.status=1)
		     GROUP BY o.promo_product_id
		 ) s
		 WHERE q.promotion_product_id = s.ppid`, orderIDs)
	return err
}

// IncrViews 活动页访问量 +1。
func (p *Promotions) IncrViews(ctx context.Context, promotionID int64) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO promotion_stats(promotion_id,views,claimed,orders,paid_amount)
		 VALUES($1,1,0,0,0)
		 ON CONFLICT(promotion_id) DO UPDATE SET views=promotion_stats.views+1`,
		promotionID)
	return err
}

// GetStats 获取活动统计。
func (p *Promotions) GetStats(ctx context.Context, promotionID int64) (views, claimed, orders int, paidAmount float64, err error) {
	err = p.db.QueryRowContext(ctx,
		`SELECT coalesce(views,0),coalesce(claimed,0),coalesce(orders,0),coalesce(paid_amount,0)
		   FROM promotion_stats WHERE promotion_id=$1`, promotionID).
		Scan(&views, &claimed, &orders, &paidAmount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, 0, nil
	}
	return
}

// GetQuota 获取限量名额信息。
func (p *Promotions) GetQuota(ctx context.Context, promotionProductID int64) (total, sold int, err error) {
	err = p.db.QueryRowContext(ctx,
		`SELECT coalesce(total,-1), coalesce(sold,0) FROM promotion_quota WHERE promotion_product_id=$1`,
		promotionProductID).Scan(&total, &sold)
	if errors.Is(err, sql.ErrNoRows) {
		return -1, 0, nil
	}
	return
}

// ClaimCoupon 记录用户领券（UNIQUE 约束防重复）。
func (p *Promotions) ClaimCoupon(ctx context.Context, promotionID, userID, couponID int64) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO promotion_coupon_claims(promotion_id,user_id,coupon_id) VALUES($1,$2,$3)`,
		promotionID, userID, couponID)
	return err
}

// HasClaimedCoupon 检查用户是否已领该活动券。
func (p *Promotions) HasClaimedCoupon(ctx context.Context, promotionID, userID int64) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM promotion_coupon_claims WHERE promotion_id=$1 AND user_id=$2`,
		promotionID, userID).Scan(&n)
	return n > 0, err
}

// UserCouponClaim 用户领取的活动券信息。
type UserCouponClaim struct {
	PromotionID   int64
	PromotionName string
	CouponID      int64
	CouponCode    string
	CouponType    string
	CouponValue   float64
	MinAmount     float64
	ExpiresAt     sql.NullTime
	ClaimedAt     time.Time
	Used          bool
}

// ListUserCoupons 查询用户领取的所有活动券。
func (p *Promotions) ListUserCoupons(ctx context.Context, userID int64) ([]UserCouponClaim, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT pr.id, pr.name, c.id, c.code, c.type, c.value, c.min_amount, c.expires_at,
		        cc.claimed_at,
		        EXISTS(SELECT 1 FROM coupon_usages cu WHERE cu.coupon_id=c.id AND cu.user_id=$1) AS used
		   FROM promotion_coupon_claims cc
		   JOIN promotions pr ON pr.id=cc.promotion_id
		   JOIN coupons c ON c.id=cc.coupon_id
		  WHERE cc.user_id=$1
		  ORDER BY cc.claimed_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserCouponClaim
	for rows.Next() {
		var u UserCouponClaim
		if err := rows.Scan(&u.PromotionID, &u.PromotionName, &u.CouponID, &u.CouponCode,
			&u.CouponType, &u.CouponValue, &u.MinAmount, &u.ExpiresAt, &u.ClaimedAt, &u.Used); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IsNewUser 检查用户是否新客（历史订单数为 0）。
func (p *Promotions) IsNewUser(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM orders WHERE user_id=$1`, userID).Scan(&n)
	return n == 0, err
}

// CheckLimitPerUser 校验用户在该活动的下单数是否超限。
func (p *Promotions) CheckLimitPerUser(ctx context.Context, tx *sql.Tx, promotionID, userID int64, limit int) error {
	if limit <= 0 {
		return nil
	}
	var n int
	// 这里不能加 FOR UPDATE：PostgreSQL 禁止聚合函数与行锁同用，语句会直接报错，
	// 导致凡配置了「每人限购」的活动一单都下不了。
	// 并发安全由调用方保证：CreateOrder 在同一事务内先取 pg_advisory_xact_lock('user_order_lock:'||userID)，
	// 而限购是按 (活动, 用户) 判定的，同一用户的下单事务已被串行化。
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM orders WHERE promotion_id=$1 AND user_id=$2`, promotionID, userID).Scan(&n); err != nil {
		return err
	}
	if n >= limit {
		return ErrPromotionLimitExceeded
	}
	return nil
}

var (
	ErrPromotionSoldOut       = errors.New("活动名额已售罄")
	ErrPromotionLimitExceeded = errors.New("超出该活动每人限购数量")
)
