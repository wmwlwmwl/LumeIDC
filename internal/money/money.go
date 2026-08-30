package money

import (
	"errors"
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
	return strconv.FormatInt(cents/100, 10) + "." + strconv.FormatInt(cents%100, 10), cents, nil
}

func FiniteNonNegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
