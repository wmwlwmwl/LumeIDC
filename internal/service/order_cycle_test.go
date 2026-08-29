package service

import (
	"testing"
	"time"
)

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
