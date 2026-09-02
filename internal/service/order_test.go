package service

import "testing"

func TestApplyProfit(t *testing.T) {
	cases := []struct {
		cost  float64
		ptype int16
		pval  float64
		want  float64
	}{
		{7.00, 0, 100, 14.00},     // 百分比 100%：成本翻倍
		{7.00, 0, 50, 10.50},      // 百分比 50%
		{7.00, 0, 0, 7.00},        // 0 不加成
		{7.00, 0, -10, 7.00},      // 负值不加成（防负价）
		{7.00, 1, 3, 10.00},       // 固定金额 +3
		{0.00, 0, 100, 0.00},      // 成本 0（纯配置计价产品的季付路径不会走到，但求稳）
		{12.34, 0, 12.5, 13.8825}, // 非整值不在此四舍五入（mathRound 由调用方负责）
	}
	for i, c := range cases {
		if got := applyProfit(c.cost, c.ptype, c.pval); got != c.want {
			t.Errorf("case %d: applyProfit(%v,%d,%v)=%v want %v", i, c.cost, c.ptype, c.pval, got, c.want)
		}
	}
}
