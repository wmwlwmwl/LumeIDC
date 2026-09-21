package service

import (
	"testing"
)

func TestValidatePromotionRules(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		rules   string
		wantErr bool
	}{
		{"折扣正常", "discount", `{"price":99}`, false},
		{"折扣负价", "discount", `{"price":-1}`, true},
		{"折扣缺价", "discount", `{}`, true},
		{"抢购正常", "flash_sale", `{"price":9.9,"stock":50}`, false},
		{"抢购名额为零", "flash_sale", `{"price":9.9,"stock":0}`, true},
		{"抢购名额非整数", "flash_sale", `{"price":9.9,"stock":1.5}`, true},
		{"满减正常", "full_reduction", `{"threshold":200,"reduce":50}`, false},
		{"满减减到0", "full_reduction", `{"threshold":50,"reduce":50}`, true},
		{"满减减穿", "full_reduction", `{"threshold":50,"reduce":99}`, true},
		{"领券正常", "coupon_giveaway", `{"coupon_id":12}`, false},
		{"领券缺券", "coupon_giveaway", `{}`, true},
		{"新客正常", "new_user", `{"price":1}`, false},
		{"非法JSON", "discount", `{bad`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePromotionRules(c.typ, []byte(c.rules))
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidatePromotionRules(%s, %s) err=%v, wantErr=%v", c.typ, c.rules, err, c.wantErr)
			}
		})
	}
}
