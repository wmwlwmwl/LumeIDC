package handler

import "testing"

func TestPromotionContextFromForm(t *testing.T) {
	cases := []struct {
		name           string
		form           map[string]string
		wantPromoID    int64
		wantPromoProd int64
	}{
		{"未带活动字段", map[string]string{"coupon": "X"}, 0, 0},
		{"正常活动上下文", map[string]string{"promotion_id": "12", "promotion_product_id": "34"}, 12, 34},
		{"空白字符也按未指定处理", map[string]string{"promotion_id": "   ", "promotion_product_id": ""}, 0, 0},
		{"非数字归零", map[string]string{"promotion_id": "abc", "promotion_product_id": "1e3"}, 0, 0},
		{"零与非正数归零", map[string]string{"promotion_id": "0", "promotion_product_id": "-5"}, 0, 0},
		{"负数归零", map[string]string{"promotion_id": "-12", "promotion_product_id": "-34"}, 0, 0},
		{"只带一半时另一半仍归零", map[string]string{"promotion_id": "12"}, 12, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotID, gotProductID := promotionContextFromForm(func(k string) string { return c.form[k] })
			if gotID != c.wantPromoID || gotProductID != c.wantPromoProd {
				t.Fatalf("promotionContextFromForm(%v) = (%d, %d), want (%d, %d)",
					c.form, gotID, gotProductID, c.wantPromoID, c.wantPromoProd)
			}
		})
	}
}
