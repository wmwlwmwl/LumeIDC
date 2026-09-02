package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("金额无效")

// ParsePositive parses a non-exponent decimal amount with at most two decimals.
func ParsePositive(s string, maxCents int64) (string, int64, error) {
	if s == "" || strings.TrimSpace(s) != s || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return "", 0, ErrInvalid
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 10 {
		return "", 0, ErrInvalid
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return "", 0, ErrInvalid
		}
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			return "", 0, ErrInvalid
		}
		for _, r := range frac {
			if r < '0' || r > '9' {
				return "", 0, ErrInvalid
			}
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", 0, ErrInvalid
	}
	cents := whole * 100
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return "", 0, ErrInvalid
	}
	cents += f
	if cents <= 0 || cents > maxCents {
		return "", 0, ErrInvalid
	}
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64), cents, nil
}

func FiniteNonNegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

// ParsePercent parses a non-negative percentage with at most two decimals.
// The returned integer is hundredths of a percentage point (2.50% => 250).
func ParsePercent(s string) (string, int64, error) {
	if s == "" {
		return "0.00", 0, nil
	}
	if strings.TrimSpace(s) != s || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return "", 0, ErrInvalid
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 3 {
		return "", 0, ErrInvalid
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return "", 0, ErrInvalid
		}
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			return "", 0, ErrInvalid
		}
		for _, r := range frac {
			if r < '0' || r > '9' {
				return "", 0, ErrInvalid
			}
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", 0, ErrInvalid
	}
	basis := whole*100 + mustParseInt(frac)
	if basis < 0 || basis > 10000 {
		return "", 0, ErrInvalid
	}
	return fmt.Sprintf("%d.%02d", basis/100, basis%100), basis, nil
}

func mustParseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// AddPercent calculates a payment amount using integer cents. The fee is
// rounded half up to the nearest cent and the returned strings have 2 decimals.
func AddPercent(amount, percent string) (fee, payable string, err error) {
	_, baseCents, err := ParsePositive(amount, 999999999999)
	if err != nil {
		return "", "", ErrInvalid
	}
	_, rateBasis, err := ParsePercent(percent)
	if err != nil {
		return "", "", ErrInvalid
	}
	if rateBasis > 0 && baseCents > (math.MaxInt64-5000)/rateBasis {
		return "", "", ErrInvalid
	}
	feeCents := int64(0)
	if rateBasis > 0 {
		feeCents = (baseCents*rateBasis + 5000) / 10000
	}
	if feeCents > 999999999999-baseCents {
		return "", "", ErrInvalid
	}
	return formatCents(feeCents), formatCents(baseCents + feeCents), nil
}

func formatCents(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
