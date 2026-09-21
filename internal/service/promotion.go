package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"lumeidc/internal/repo"
)

// PromotionService 营销活动服务（计价 + 校验）。
type PromotionService struct {
	db      *sql.DB
	Promo   *repo.Promotions
	Coupons *repo.Coupons
}

// NewPromotionService 创建活动服务。
func NewPromotionService(db *sql.DB, promo *repo.Promotions, coupons *repo.Coupons) *PromotionService {
	return &PromotionService{db: db, Promo: promo, Coupons: coupons}
}

// ActivePromotion 商品命中的生效活动。
type ActivePromotion struct {
	PromotionID        int64
	PromotionProductID int64
	Type               string
	Name               string
	LimitPerUser       int
	QuotaTotal         int
	QuotaSold          int
	// 各类型规则参数
	Price     float64 // discount / flash_sale / new_user 的活动价
	Threshold float64 // full_reduction 满减门槛
	Reduce    float64 // full_reduction 减免金额
	CouponID  int64   // coupon_giveaway 关联优惠券
	RulesRaw  json.RawMessage
}

// activePromotionFromRepo 把 repo 层命中的活动转成活动服务模型并解析规则参数。
func (s *PromotionService) activePromotionFromRepo(ap *repo.ActivePromotion) *ActivePromotion {
	out := &ActivePromotion{
		PromotionID:        ap.Promotion.ID,
		PromotionProductID: ap.Product.ID,
		Type:               ap.Promotion.Type,
		Name:               ap.Promotion.Name,
		LimitPerUser:       ap.Promotion.LimitPerUser,
		QuotaTotal:         ap.QuotaTotal,
		QuotaSold:          ap.QuotaSold,
		RulesRaw:           ap.Product.Rules,
	}
	// 解析规则参数
	var rules map[string]any
	if len(ap.Product.Rules) > 0 {
		_ = json.Unmarshal(ap.Product.Rules, &rules)
	}
	switch ap.Promotion.Type {
	case "discount", "flash_sale", "new_user":
		if v, ok := rules["price"].(float64); ok {
			out.Price = v
		}
	case "full_reduction":
		if v, ok := rules["threshold"].(float64); ok {
			out.Threshold = v
		}
		if v, ok := rules["reduce"].(float64); ok {
			out.Reduce = v
		}
	case "coupon_giveaway":
		if v, ok := rules["coupon_id"].(float64); ok {
			out.CouponID = int64(v)
		}
	}
	return out
}

// ActivePromotionFor 查询商品当前生效的活动。
func (s *PromotionService) ActivePromotionFor(ctx context.Context, productID, pricesetID int64, cycle string) (*ActivePromotion, error) {
	ap, err := s.Promo.ActivePromotionFor(ctx, productID, pricesetID, cycle)
	if err != nil || ap == nil {
		return nil, err
	}
	return s.activePromotionFromRepo(ap), nil
}

// ErrPromotionBindingMismatch 客户端指定的活动绑定与服务端校验结果不一致。
var ErrPromotionBindingMismatch = errors.New("活动信息已失效，请刷新后重试")

// ActivePromotionForRequested 校验并采纳购买页指定的活动绑定。
// 前端从活动详情页进入购买页时会带上 promotion_id / promotion_product_id；
// 服务端必须确认该绑定确实属于「当前商品 + 价格组 + 周期」且活动已启用并在进行中，
// 任一条件不满足（含 ID 非正数）一律拒绝，绝不在服务端静默改判为其他活动。
func (s *PromotionService) ActivePromotionForRequested(ctx context.Context, productID, pricesetID int64, cycle string, promotionID, promotionProductID int64) (*ActivePromotion, error) {
	if promotionID <= 0 || promotionProductID <= 0 {
		return nil, fmt.Errorf("%w", ErrPromotionBindingMismatch)
	}
	ap, err := s.Promo.ActivePromotionBindingFor(ctx, promotionID, promotionProductID, productID, pricesetID, cycle)
	if err != nil {
		return nil, err
	}
	if ap == nil {
		return nil, fmt.Errorf("%w", ErrPromotionBindingMismatch)
	}
	if ap.Promotion.ID != promotionID || ap.Product.ID != promotionProductID || ap.Product.ProductID != productID {
		return nil, fmt.Errorf("%w", ErrPromotionBindingMismatch)
	}
	return s.activePromotionFromRepo(ap), nil
}

var ErrUnsupportedPromotionType = errors.New("暂不支持该活动类型")

// ValidatePromotionType 校验活动类型是否已实现。
// 数据库 CHECK 还允许 bogo / group_buy，但业务未实现，必须在入口拒绝，避免配置出用户可见却无法生效的活动。
func ValidatePromotionType(promoType string) error {
	switch promoType {
	case "discount", "flash_sale", "full_reduction", "new_user", "coupon_giveaway":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPromotionType, promoType)
	}
}

func (s *PromotionService) ValidatePromotionType(promoType string) error {
	return ValidatePromotionType(promoType)
}

// ApplyPromotion 对原售价应用活动规则。
// 输入：原售价 sell（利润加成后）、活动类型、规则参数
// 输出：最终金额 finalAmount、优惠金额 discount
// 注意：活动价订单不叠加优惠券（full_reduction / coupon_giveaway 除外）。
func (s *PromotionService) ApplyPromotion(sell float64, promo *ActivePromotion) (finalAmount, discount float64) {
	if promo == nil {
		return sell, 0
	}
	switch promo.Type {
	case "discount", "flash_sale", "new_user":
		// 直接用活动价覆盖
		if promo.Price > 0 && promo.Price < sell {
			return promo.Price, math.Round((sell-promo.Price)*100) / 100
		}
		return sell, 0
	case "full_reduction":
		// 满减：达到门槛则减免
		if promo.Threshold > 0 && promo.Reduce > 0 && sell >= promo.Threshold {
			reduced := sell - promo.Reduce
			if reduced < 0 {
				reduced = 0
			}
			actualDiscount := sell - reduced
			return math.Round(reduced*100) / 100, math.Round(actualDiscount*100) / 100
		}
		return sell, 0
	case "coupon_giveaway":
		// 不改价格，下单时走优惠券抵扣
		return sell, 0
	}
	return sell, 0
}

// CheckNewUser 校验是否新客。
func (s *PromotionService) CheckNewUser(ctx context.Context, userID int64) (bool, error) {
	return s.Promo.IsNewUser(ctx, userID)
}

func (s *PromotionService) CheckNewUserTx(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	return s.Promo.IsNewUserTx(ctx, tx, userID)
}

func (s *PromotionService) CheckPromotionActiveTx(ctx context.Context, tx *sql.Tx, promotionID int64) error {
	return s.Promo.CheckPromotionActiveTx(ctx, tx, promotionID)
}

// CheckQuotaAndLimit 下单时校验名额 + 限购（事务内调用）。
// promoType 为 flash_sale 时锁定名额；limit_per_user > 0 时校验下单数。
func (s *PromotionService) CheckQuotaAndLimit(ctx context.Context, tx *sql.Tx, promo *ActivePromotion, userID int64, limitPerUser int) error {
	// 限量抢购：锁定名额
	if promo.Type == "flash_sale" {
		if err := s.Promo.TryReserveQuota(ctx, tx, promo.PromotionProductID); err != nil {
			return err
		}
	}
	// 限购校验
	if limitPerUser > 0 {
		if err := s.Promo.CheckLimitPerUser(ctx, tx, promo.PromotionID, userID, limitPerUser); err != nil {
			// 名额已锁定，回滚时由事务释放
			return err
		}
	}
	return nil
}

func (s *PromotionService) ClaimCoupon(ctx context.Context, promotionID, promotionProductID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var startsAt, endsAt time.Time
	err = tx.QueryRowContext(ctx,
		`SELECT starts_at,ends_at FROM promotions WHERE id=$1 AND type='coupon_giveaway' AND enabled=true FOR UPDATE`,
		promotionID).Scan(&startsAt, &endsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("活动不存在或未启用")
	}
	if err != nil {
		return err
	}
	if time.Now().Before(startsAt) || !time.Now().Before(endsAt) {
		return errors.New("当前不在活动领取时间内")
	}

	var claimedCouponID int64
	err = tx.QueryRowContext(ctx,
		`SELECT coupon_id FROM promotion_coupon_claims WHERE promotion_id=$1 AND user_id=$2 FOR UPDATE`,
		promotionID, userID).Scan(&claimedCouponID)
	if err == nil {
		return errors.New("您已领取过该活动优惠券")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var templateCouponID int64
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE((rules->>'coupon_id')::bigint,0)
		   FROM promotion_products
		  WHERE id=$1 AND promotion_id=$2
		  FOR UPDATE`,
		promotionProductID, promotionID).Scan(&templateCouponID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("活动商品不存在")
	}
	if err != nil {
		return err
	}
	if templateCouponID == 0 {
		return errors.New("活动未配置优惠券")
	}

	var tpl repo.Coupon
	var expires sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT id,type,value,min_amount,expires_at,active FROM coupons WHERE id=$1 FOR UPDATE`,
		templateCouponID).Scan(&tpl.ID, &tpl.Type, &tpl.Value, &tpl.MinAmount, &expires, &tpl.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("活动优惠券模板不存在")
	}
	if err != nil {
		return err
	}
	if !tpl.Active {
		return errors.New("活动优惠券已失效")
	}
	if expires.Valid && !expires.Time.After(time.Now()) {
		return errors.New("活动优惠券已过期")
	}

	var userCouponID int64
	userCode := fmt.Sprintf("PROMO_%d_%d", promotionID, userID)
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO coupons(code,type,value,min_amount,usage_limit,expires_at,active,user_id) VALUES($1,$2,$3,$4,1,$5,true,$6) ON CONFLICT DO NOTHING RETURNING id`,
		userCode, tpl.Type, tpl.Value, tpl.MinAmount, expires, userID).Scan(&userCouponID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("您已领取过该活动优惠券")
		}
		return fmt.Errorf("生成专属优惠码失败: %w", err)
	}
	var claimID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO promotion_coupon_claims(promotion_id,user_id,coupon_id) VALUES($1,$2,$3) ON CONFLICT (promotion_id,user_id) DO NOTHING RETURNING id`,
		promotionID, userID, userCouponID).Scan(&claimID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("您已领取过该活动优惠券")
	}
	if err != nil {
		return err
	}
	if err := s.Promo.IncrementClaimedTx(ctx, tx, promotionID); err != nil {
		return err
	}
	return tx.Commit()
}

// formatPromoAmount 格式化金额字符串（两位小数）。
func formatPromoAmount(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64)
}

// ListUserCoupons 查询用户领取的活动券列表。
func (s *PromotionService) ListUserCoupons(ctx context.Context, userID int64) ([]repo.UserCouponClaim, error) {
	return s.Promo.ListUserCoupons(ctx, userID)
}

// ErrPromotionNotApplicable 活动不适用（如新客活动对老用户）。
var ErrPromotionNotApplicable = errors.New("该活动不适用于您的账户")

// ValidatePromotionApplicable 校验活动是否适用于该用户（如新客专享）。
func (s *PromotionService) ValidatePromotionApplicable(ctx context.Context, promo *ActivePromotion, userID int64) error {
	if promo.Type == "new_user" {
		isNew, err := s.CheckNewUser(ctx, userID)
		if err != nil {
			return fmt.Errorf("校验新客状态失败: %w", err)
		}
		if !isNew {
			return ErrPromotionNotApplicable
		}
	}
	return nil
}

func (s *PromotionService) ValidatePromotionApplicableTx(ctx context.Context, tx *sql.Tx, promo *ActivePromotion, userID int64) error {
	if promo.Type == "new_user" {
		isNew, err := s.CheckNewUserTx(ctx, tx, userID)
		if err != nil {
			return fmt.Errorf("校验新客状态失败: %w", err)
		}
		if !isNew {
			return ErrPromotionNotApplicable
		}
	}
	return nil
}
