package service

import (
	"testing"
	"time"
)

func TestAvailableCycles(t *testing.T) {
	cases := []struct {
		name         string
		m, q, y      float64
		cycles       []string
		defaultCycle string
	}{
		{"月付优先", 10, 20, 30, []string{"monthly", "quarterly", "yearly"}, "monthly"},
		{"仅年付", 0, 0, 120, []string{"yearly"}, "yearly"},
		{"无价格", 0, 0, 0, nil, ""},
	}
	for _, c := range cases {
		got, def := AvailableCycles(c.m, c.q, c.y)
		if def != c.defaultCycle || len(got) != len(c.cycles) {
			t.Fatalf("%s: got %v/%s want %v/%s", c.name, got, def, c.cycles, c.defaultCycle)
		}
		for i := range got {
			if got[i] != c.cycles[i] {
				t.Fatalf("%s: got %v want %v", c.name, got, c.cycles)
			}
		}
	}
}

func TestCycleDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"monthly":   30 * 24 * time.Hour,
		"quarterly": 90 * 24 * time.Hour,
		"yearly":    365 * 24 * time.Hour,
	}
	for c, want := range cases {
		got, err := CycleDuration(c)
		if err != nil || got != want {
			t.Fatalf("cycle %s: got %v err %v", c, got, err)
		}
	}
	if _, err := CycleDuration("weekly"); err == nil {
		t.Fatal("无效周期应报错")
	}
}
