package service

import (
	"math"
	"testing"
)

// 同周期升级：月付 30 → 月付 90，剩余 15 天 → (90/30 − 30/30) × 15 = 30
func TestProratedDiffSameCycle(t *testing.T) {
	got, err := ProratedDiff(30, "monthly", 90, "monthly", 15)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if math.Abs(got-30) > 0.005 {
		t.Fatalf("期望 30.00，实得 %.2f", got)
	}
}

// 换周期（月付 → 年付）必须按各自周期折算日价。
// 旧实现固定按月口径相减会得到 300−30=270，与实际年付严重不符。
func TestProratedDiffMonthToYear(t *testing.T) {
	got, err := ProratedDiff(30, "monthly", 300, "yearly", 10)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 300/365×10 − 30/30×10 = 8.22 − 10 = −1.78
	if math.Abs(got+1.78) > 0.005 {
		t.Fatalf("期望 -1.78，实得 %.2f", got)
	}
}

// 反向：年付日价更低，改回月付反而要补款（符合"月付单价更贵"的直觉）。
func TestProratedDiffYearToMonth(t *testing.T) {
	got, err := ProratedDiff(300, "yearly", 30, "monthly", 100)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 30/30×100 − 300/365×100 = 100 − 82.19 = 17.81
	if math.Abs(got-17.81) > 0.005 {
		t.Fatalf("期望 17.81，实得 %.2f", got)
	}
}

// 服务已到期（剩余 0 或负）：必须拒绝，不能按 0 差价放行。
func TestProratedDiffExpired(t *testing.T) {
	for _, d := range []float64{0, -1} {
		if _, err := ProratedDiff(30, "monthly", 90, "monthly", d); err == nil {
			t.Fatalf("剩余 %.0f 天应报错", d)
		}
	}
}

// 未知周期：直接报错，避免拿 0 当除数算出 Inf。
func TestProratedDiffInvalidCycle(t *testing.T) {
	if _, err := ProratedDiff(30, "weekly", 90, "monthly", 10); err == nil {
		t.Fatal("当前周期未知时应报错")
	}
	if _, err := ProratedDiff(30, "monthly", 90, "weekly", 10); err == nil {
		t.Fatal("目标周期未知时应报错")
	}
}

func TestCycleDays(t *testing.T) {
	for cycle, want := range map[string]float64{"monthly": 30, "quarterly": 90, "yearly": 365} {
		if got := CycleDays(cycle); got != want {
			t.Fatalf("%s 期望 %v 天，实得 %v", cycle, want, got)
		}
	}
	if CycleDays("weekly") != 0 {
		t.Fatal("未知周期应返回 0 天")
	}
}
