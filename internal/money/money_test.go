package money

import "testing"

func TestParsePositive(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"0.01", true}, {"12.5", true}, {"9999999999.99", true},
		{"", false}, {"0", false}, {"-1", false}, {"+1", false},
		{"NaN", false}, {"Inf", false}, {"1e2", false}, {"1.234", false},
		{" 1", false}, {"10000000000", false},
	} {
		_, _, err := ParsePositive(tc.in, 999999999999)
		if (err == nil) != tc.ok {
			t.Fatalf("ParsePositive(%q) ok=%v, want %v", tc.in, err == nil, tc.ok)
		}
	}
}

func TestParsePositiveKeepsTwoDecimalPlaces(t *testing.T) {
	for in, want := range map[string]string{"0.01": "0.01", "0.10": "0.10", "12.5": "12.50"} {
		got, _, err := ParsePositive(in, 999999999999)
		if err != nil || got != want {
			t.Fatalf("ParsePositive(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestFiniteNonNegative(t *testing.T) {
	if !FiniteNonNegative(0) || !FiniteNonNegative(1.2) || FiniteNonNegative(-1) {
		t.Fatal("finite non-negative validation failed")
	}
}

func TestParsePercent(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "0.00", true}, {"0", "0.00", true}, {"2.5", "2.50", true}, {"100.00", "100.00", true},
		{"-1", "", false}, {"101", "", false}, {"1.234", "", false}, {"1e2", "", false}, {" 1", "", false},
	} {
		got, _, err := ParsePercent(tc.in)
		if (err == nil) != tc.ok || tc.ok && got != tc.want {
			t.Errorf("ParsePercent(%q) = %q, %v; want %q, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestAddPercent(t *testing.T) {
	for _, tc := range []struct {
		amount, percent, fee, payable string
	}{
		{"10.00", "2.50", "0.25", "10.25"},
		{"0.03", "50", "0.02", "0.05"},
		{"0.01", "1", "0.00", "0.01"},
		{"12.34", "0", "0.00", "12.34"},
	} {
		fee, payable, err := AddPercent(tc.amount, tc.percent)
		if err != nil || fee != tc.fee || payable != tc.payable {
			t.Errorf("AddPercent(%q,%q) = %q,%q,%v; want %q,%q", tc.amount, tc.percent, fee, payable, err, tc.fee, tc.payable)
		}
	}
}
