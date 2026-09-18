package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

// promotionList GET /promotions — 已启用活动列表（进行中 + 即将开始）。
func (h *Pages) promotionList(w http.ResponseWriter, r *http.Request) {
	if h.Promotion == nil || h.Promotion.Promo == nil {
		writeJSON(w, map[string]any{"ok": 1, "list": []any{}})
		return
	}
	all, err := h.Promotion.Promo.List(r.Context())
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	now := time.Now()
	list := make([]map[string]any, 0, len(all))
	for _, p := range all {
		if !p.Enabled || now.After(p.EndsAt) {
			continue
		}
		list = append(list, map[string]any{
			"id":          p.ID,
			"name":        p.Name,
			"description": p.Description,
			"type":        p.Type,
			"banner":      p.Banner,
			"starts_at":   p.StartsAt.Format(time.RFC3339),
			"ends_at":     p.EndsAt.Format(time.RFC3339),
			"status":      p.Status(),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}

// promotionDetail GET /promotion/{id} — 活动详情页数据。
func (h *Pages) promotionDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if h.Promotion == nil || h.Promotion.Promo == nil {
		jsonStatus(w, r, 500, "活动模块未启用")
		return
	}
	promo, err := h.Promotion.Promo.Get(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 404, "活动不存在")
		return
	}
	// 访问量 +1
	_ = h.Promotion.Promo.IncrViews(r.Context(), id)

	claimed := false
	if sess := middleware.FromSession(r.Context()); sess != nil && !sess.IsAdmin && sess.UserID > 0 {
		claimed, err = h.Promotion.Promo.HasClaimedCoupon(r.Context(), id, sess.UserID)
		if err != nil {
			jsonStatus(w, r, 500, "查询失败")
			return
		}
	}

	products, err := h.Promotion.Promo.ListProducts(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}

	// 批量获取商品原价
	ids := make([]int64, 0, len(products))
	for _, pp := range products {
		ids = append(ids, pp.ProductID)
	}
	pricesetID, err := h.Products.DefaultPricesetID(r.Context())
	if err != nil {
		jsonStatus(w, r, 500, "查询价格组失败")
		return
	}
	prices := h.Products.PricesByProduct(r.Context(), pricesetID, ids)

	// 获取商品基本信息
	prodMap := make(map[int64]string)
	for _, pid := range ids {
		if p, err := h.Products.Get(r.Context(), pid); err == nil {
			prodMap[pid] = p.Name
		}
	}

	// 计算活动价
	cards := make([]map[string]any, 0, len(products))
	for _, pp := range products {
		price, ok := prices[pp.ProductID]
		if !ok {
			continue
		}
		// 解析规则
		var rules map[string]any
		_ = json.Unmarshal(pp.Rules, &rules)

		card := map[string]any{
			"product_id": pp.ProductID,
			"name":       prodMap[pp.ProductID],
			"cycle":      pp.Cycle.String,
		}
		// 原价
		origMonthly := 0.0
		if m, err := strconv.ParseFloat(price.Monthly, 64); err == nil {
			origMonthly = m
		}
		card["original_price"] = price.Monthly

		// 根据活动类型计算活动价
		ap := &service.ActivePromotion{
			PromotionID:        promo.ID,
			PromotionProductID: pp.ID,
			Type:               promo.Type,
			Name:               promo.Name,
			LimitPerUser:       promo.LimitPerUser,
		}
		switch promo.Type {
		case "discount", "flash_sale", "new_user":
			if v, ok := rules["price"].(float64); ok {
				ap.Price = v
				card["promo_price"] = v
				card["discount"] = round2(origMonthly - v)
			}
		case "full_reduction":
			if v, ok := rules["threshold"].(float64); ok {
				ap.Threshold = v
				card["threshold"] = v
			}
			if v, ok := rules["reduce"].(float64); ok {
				ap.Reduce = v
				card["reduce"] = v
			}
			// 满减后的预估价格（按月价算）
			if origMonthly >= ap.Threshold && ap.Reduce > 0 {
				card["promo_price"] = round2(origMonthly - ap.Reduce)
			} else {
				card["promo_price"] = origMonthly
			}
		case "coupon_giveaway":
			if v, ok := rules["coupon_id"].(float64); ok {
				ap.CouponID = int64(v)
				card["coupon_id"] = ap.CouponID
			}
			card["promo_price"] = origMonthly
		}

		// 限量抢购库存
		if promo.Type == "flash_sale" {
			total, sold, _ := h.Promotion.Promo.GetQuota(r.Context(), pp.ID)
			card["quota_total"] = total
			card["quota_sold"] = sold
			card["quota_left"] = total - sold
			card["sold_out"] = total >= 0 && sold >= total
		}

		cards = append(cards, card)
	}

	writeJSON(w, map[string]any{
		"ok": 1,
		"promotion": map[string]any{
			"id":             promo.ID,
			"name":           promo.Name,
			"description":    promo.Description,
			"type":           promo.Type,
			"banner":         promo.Banner,
			"notice":         promo.Notice,
			"rules_text":     promo.RulesText,
			"starts_at":      promo.StartsAt.Format(time.RFC3339),
			"ends_at":        promo.EndsAt.Format(time.RFC3339),
			"enabled":        promo.Enabled,
			"limit_per_user": promo.LimitPerUser,
			"status":         promo.Status(),
			"coupon_claimed": claimed,
		},
		"products": cards,
	})
}

// promotionClaim POST /promotion/{id}/claim — 领取活动优惠券。
func (h *Pages) promotionClaim(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	promo, err := h.Promotion.Promo.Get(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 404, "活动不存在")
		return
	}
	if promo.Type != "coupon_giveaway" {
		jsonStatus(w, r, 400, "该活动无优惠券可领")
		return
	}
	// 取第一个绑定商品的 coupon_id
	products, err := h.Promotion.Promo.ListProducts(r.Context(), id)
	if err != nil || len(products) == 0 {
		jsonStatus(w, r, 400, "活动配置错误")
		return
	}
	var rules map[string]any
	_ = json.Unmarshal(products[0].Rules, &rules)
	couponID, _ := rules["coupon_id"].(float64)
	if couponID == 0 {
		jsonStatus(w, r, 400, "活动未配置优惠券")
		return
	}
	if err := h.Promotion.ClaimCoupon(r.Context(), id, userID, int64(couponID)); err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "领取成功"})
}

// userPromotionCoupons GET /user/promotion-coupons — 用户领取的活动券列表。
func (h *Pages) userPromotionCoupons(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	list, err := h.Promotion.ListUserCoupons(r.Context(), userID)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		item := map[string]any{
			"promotion_id":   c.PromotionID,
			"promotion_name": c.PromotionName,
			"coupon_code":    c.CouponCode,
			"coupon_type":    c.CouponType,
			"coupon_value":   c.CouponValue,
			"min_amount":     c.MinAmount,
			"claimed_at":     c.ClaimedAt.Format("2006-01-02 15:04"),
			"used":           c.Used,
		}
		if c.ExpiresAt.Valid {
			item["expires_at"] = c.ExpiresAt.Time.Format("2006-01-02")
		}
		out = append(out, item)
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
