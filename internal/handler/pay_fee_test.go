package handler

import "testing"

func TestQuoteGateway(t *testing.T) {
	cases := []struct {
		amount, rate, normalized, fee, payable string
	}{
		{"100.00", "1.50", "1.50", "1.50", "101.50"},
		{"0.03", "50", "50.00", "0.02", "0.05"},
		{"12.34", "", "0.00", "0.00", "12.34"},
	}
	for _, tc := range cases {
		feeRate, fee, payable, err := quoteGateway(tc.amount, map[string]string{"fee_percent": tc.rate})
		if err != nil || feeRate != tc.normalized || fee != tc.fee || payable != tc.payable {
			t.Errorf("quoteGateway(%q,%q) = %q,%q,%q,%v; want %q,%q,%q", tc.amount, tc.rate, feeRate, fee, payable, err, tc.normalized, tc.fee, tc.payable)
		}
	}
	if _, _, _, err := quoteGateway("100.00", map[string]string{"fee_percent": "100.01"}); err == nil {
		t.Fatal("超过 100%% 的费率应被拒绝")
	}
}
