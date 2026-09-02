package zjmf

import "testing"

func TestExtractPricingNewFormat(t *testing.T) {
	entries := []pricingEntry{
		{BillingCycle: "monthly", Monthly: 16},
		{BillingCycle: "quarterly", Quarterly: 45},
		{BillingCycle: "annually", Annually: 160},
	}
	m, q, y := extractPricing(entries, nil)
	if m != 16 || q != 45 || y != 160 {
		t.Fatalf("got %v %v %v", m, q, y)
	}
}

func TestExtractPricingLegacy(t *testing.T) {
	legacy := map[string]jsonNum{"monthly": 5, "quarterly": 14, "annually": 50}
	m, q, y := extractPricing(nil, legacy)
	if m != 5 || q != 14 || y != 50 {
		t.Fatalf("got %v %v %v", m, q, y)
	}
}

func TestMinConfigMonthly(t *testing.T) {
	cases := map[string]struct {
		groups []cfgGroupField
		want   float64
	}{
		"弹性云-各档最低求和": {
			groups: []cfgGroupField{{Options: []cfgOptionField{
				{OptionType: 6, Subs: []cfgSubField{{Pricings: []pricingEntry{{Monthly: 1.5}, {Monthly: 3}}}, {Pricings: []pricingEntry{{Monthly: 2.5}}}}}, // CPU 最低 1.5
				{OptionType: 8, Subs: []cfgSubField{{Pricings: []pricingEntry{{Monthly: 2.5}}}, {Pricings: []pricingEntry{{Monthly: 5}}}}},                 // 内存最低 2.5
				{OptionType: 5, Subs: []cfgSubField{{Pricings: []pricingEntry{{Monthly: 8}}}}},                                                             // OS 不计价
				{Hidden: 1, Subs: []cfgSubField{{Pricings: []pricingEntry{{Monthly: 99}}}}},                                                                // 隐藏不计
				{OptionType: 4, Subs: []cfgSubField{{Pricings: []pricingEntry{{Monthly: 0}}}, {Pricings: []pricingEntry{{Monthly: 10}}}}},                  // 有免费档，最低 0
			}}},
			want: 4.0, // 1.5+2.5+0
		},
		"无配置档": {groups: nil, want: 0},
	}
	for name, c := range cases {
		if got := minConfigMonthly(c.groups); got != c.want {
			t.Fatalf("%s: got %v want %v", name, got, c.want)
		}
	}
}
