package repo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Coupons 优惠码管理。
type Coupons struct{ DB *sql.DB }

type Coupon struct {
	ID         int64
	Code       string
	Type       string // percent | fixed
	Value      float64
	MinAmount  float64
	ExpiresAt  *time.Time
	UsageLimit int
	UsedCount  int
	Active     bool
}

var (
	ErrCouponInvalid   = errors.New("优惠码无效或已失效")
	ErrCouponMin       = errors.New("订单金额未达优惠码最低消费")
	ErrCouponExhausted = errors.New("优惠码已被领完")
	ErrCouponUsed      = errors.New("该优惠码您已使用过")
)

// Validate 在事务内锁定并校验优惠码，返回优惠金额（已封顶到订单金额）。
// 调用方需负责写入 coupon_usages 并递增 used_count。
func (c *Coupons) Validate(ctx context.Context, tx *sql.Tx, code string, userID int64, orderAmount string) (couponID int64, discount string, err error) {
	var cp Coupon
	var starts time.Time
	var expires sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT id,type,value,min_amount,starts_at,expires_at,usage_limit,used_count,active
		 FROM coupons WHERE code=$1 AND starts_at<=now() FOR UPDATE`, code).
		Scan(&cp.ID, &cp.Type, &cp.Value, &cp.MinAmount, &starts, &expires, &cp.UsageLimit, &cp.UsedCount, &cp.Active)
	if errors.Is(err, sql.ErrNoRows) || !cp.Active {
		return 0, "", ErrCouponInvalid
	}
	if err != nil {
		return 0, "", err
	}
	if expires.Valid && expires.Time.Before(time.Now()) {
		return 0, "", ErrCouponInvalid
	}
	if cp.UsageLimit > 0 && cp.UsedCount >= cp.UsageLimit {
		return 0, "", ErrCouponExhausted
	}
	var used int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM coupon_usages WHERE coupon_id=$1 AND user_id=$2`, cp.ID, userID).Scan(&used); err != nil {
		return 0, "", err
	}
	if used > 0 {
		return 0, "", ErrCouponUsed
	}
	amt, _ := strconvParse(orderAmount)
	if amt < cp.MinAmount {
		return 0, "", ErrCouponMin
	}
	var d float64
	if cp.Type == "fixed" {
		d = cp.Value
	} else {
		d = amt * cp.Value / 100
	}
	if d > amt {
		d = amt
	}
	d = float64(int64(d*100+0.5)) / 100
	return cp.ID, format2(d), nil
}

// Use 写入使用记录并递增计数（在订单事务内调用）。
func (c *Coupons) Use(ctx context.Context, tx *sql.Tx, couponID, userID, orderID int64, discount string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coupon_usages(coupon_id,user_id,order_id,discount) VALUES($1,$2,$3,$4)`,
		couponID, userID, orderID, discount); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE coupons SET used_count=used_count+1 WHERE id=$1`, couponID)
	return err
}

func (c *Coupons) List(ctx context.Context) ([]Coupon, error) {
	rows, err := c.DB.QueryContext(ctx,
		`SELECT id,code,type,value,min_amount,expires_at,usage_limit,used_count,active FROM coupons ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Coupon
	for rows.Next() {
		var cp Coupon
		var expires sql.NullTime
		if err := rows.Scan(&cp.ID, &cp.Code, &cp.Type, &cp.Value, &cp.MinAmount, &expires, &cp.UsageLimit, &cp.UsedCount, &cp.Active); err != nil {
			return nil, err
		}
		if expires.Valid {
			cp.ExpiresAt = &expires.Time
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// Create 新增优惠码。expires 为空表示永不过期。
func (c *Coupons) Create(ctx context.Context, code, typ string, value, minAmount float64, usageLimit int, expires *time.Time) error {
	if code == "" {
		return errors.New("优惠码不能为空")
	}
	if typ != "fixed" {
		typ = "percent"
	}
	_, err := c.DB.ExecContext(ctx,
		`INSERT INTO coupons(code,type,value,min_amount,usage_limit,expires_at,active) VALUES($1,$2,$3,$4,$5,$6,true)`,
		code, typ, value, minAmount, usageLimit, expires)
	return err
}

func strconvParse(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func format2(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
