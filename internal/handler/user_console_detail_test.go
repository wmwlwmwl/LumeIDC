package handler

import (
	"testing"
	"time"
)

func TestParseUpstreamExpiryIn(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	// 同一无时区串在不同时区下解释为不同绝对时刻
	sh, err := parseUpstreamExpiryIn("2026-09-20 12:00:00", cst)
	if err != nil {
		t.Fatal(err)
	}
	ut, err := parseUpstreamExpiryIn("2026-09-20 12:00:00", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// +8 解释的绝对时刻比 UTC 解释早 8 小时（同一墙上时钟，东边时区更早）
	if ut.Sub(sh) != 8*time.Hour {
		t.Fatalf("同一串两种时区解释差 = %v, want 8h", ut.Sub(sh))
	}
	// RFC3339 自带偏移，时区参数不影响结果
	if r, err := parseUpstreamExpiryIn("2026-09-20T12:00:00+08:00", time.UTC); err != nil || !r.Equal(sh) {
		t.Fatalf("RFC3339 解析 = %v, err=%v, want %v", r, err, sh)
	}
	// Unix 时间戳为绝对时刻，不受时区影响
	if u, err := parseUpstreamExpiryIn("1758331200", time.UTC); err != nil || u.Unix() != 1758331200 {
		t.Fatalf("unix 解析 = %v, err=%v", u, err)
	}
	// 毫秒时间戳
	if u, err := parseUpstreamExpiryIn("1758331200000", time.UTC); err != nil || u.Unix() != 1758331200 {
		t.Fatalf("毫秒解析 = %v, err=%v", u, err)
	}
	// 仅日期布局
	if d, err := parseUpstreamExpiryIn("2026-09-20", cst); err != nil || d.Format("2006-01-02") != "2026-09-20" {
		t.Fatalf("日期解析 = %v, err=%v", d, err)
	}
	if _, err := parseUpstreamExpiryIn("not-a-time", cst); err == nil {
		t.Fatal("非法输入应返回错误")
	}
}
