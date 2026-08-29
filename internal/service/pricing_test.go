package service

import (
	"testing"

	"lumeidc/internal/repo"
)

func TestCalculateQuoteSelect(t *testing.T) {
	opts := []repo.ConfigOption{
		{Field: "os", Name: "操作系统", Mode: "select", Subs: []repo.ConfigValue{
			{Name: "CentOS7"}, {Name: "Ubuntu22"},
		}},
		{Field: "cpu", Name: "CPU", Mode: "select", Required: true, Subs: []repo.ConfigValue{
			{Name: "1核", Pricing: map[string]float64{"monthly": 0}},
			{Name: "2核", Pricing: map[string]float64{"monthly": 20}},
		}},
	}
	q, err := CalculateQuote(opts, 30, "monthly", map[string]string{"cpu": "2核", "os": "CentOS7"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 50 { // 30 base + 20 cpu; os 不计价
		t.Fatalf("期望 50，得到 %v", q.Total)
	}
}

func TestCalculateQuoteRange(t *testing.T) {
	opts := []repo.ConfigOption{
		{Field: "bw", Name: "带宽", Mode: "range", Min: 1, Max: 100, Step: 1, Unit: "Mbps",
			Subs: []repo.ConfigValue{
				{Name: "1-10", Min: 1, Max: 10, Pricing: map[string]float64{"monthly": 2}}, // 每步2元
				{Name: "11-100", Min: 11, Max: 100, Pricing: map[string]float64{"monthly": 1.5}},
			}},
	}
	q, err := CalculateQuote(opts, 30, "monthly", map[string]string{"bw": "5"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 40 { // 30 + 5步×2元 = 40
		t.Fatalf("期望 40，得到 %v", q.Total)
	}
	// 数量型（qty_min>0）按 单价 × 数量：15 × 1.5 = 22.5
	q, err = CalculateQuote(opts, 30, "monthly", map[string]string{"bw": "15"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 52.5 {
		t.Fatalf("期望 52.5，得到 %v", q.Total)
	}
	// 超界必须报错
	if _, err := CalculateQuote(opts, 30, "monthly", map[string]string{"bw": "200"}); err == nil {
		t.Fatal("超界应报错")
	}
}

func TestCalculateQuoteQuantityCPU(t *testing.T) {
	// 魔方财务 CPU：数量型（qty_min>0），单价 ¥5/核 × 所选核数。
	opts := []repo.ConfigOption{{
		Field: "cpu", Name: "CPU", Mode: "range", Min: 4, Max: 32, Step: 1, Unit: "核",
		Subs: []repo.ConfigValue{{Name: "4核", Min: 4, Max: 32, Pricing: map[string]float64{"monthly": 5}}},
	}}
	q, err := CalculateQuote(opts, 0, "monthly", map[string]string{"cpu": "4"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 20 { // 4核 × ¥5
		t.Fatalf("CPU 4核 期望 20，得到 %v", q.Total)
	}
	q, err = CalculateQuote(opts, 0, "monthly", map[string]string{"cpu": "8"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 40 { // 8核 × ¥5
		t.Fatalf("CPU 8核 期望 40，得到 %v", q.Total)
	}
}

func TestCalculateQuoteDataDiskRange(t *testing.T) {
	// 数据盘：跨度子项 0|0G(0~120GB, ¥0.30/GB) + 离散整包 20|20G(=¥10)。
	opts := []repo.ConfigOption{{
		Field: "data_disk", Name: "数据盘", Mode: "range", Min: 0, Max: 120, Step: 1, Unit: "G",
		Subs: []repo.ConfigValue{
			{Name: "0G", Min: 0, Max: 120, Pricing: map[string]float64{"monthly": 0.30}},
			{Name: "20G", Min: 0, Max: 0, Pricing: map[string]float64{"monthly": 10}},
		},
	}}
	// 3GB 无精确整包 → 命中跨度子项，单价×数量 = 0.30×3 = 0.90
	q, err := CalculateQuote(opts, 0, "monthly", map[string]string{"data_disk": "3"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 0.90 {
		t.Fatalf("数据盘 3G 期望 0.90，得到 %v", q.Total)
	}
	// 20GB 命中精确整包 → 平铺 ¥10
	q, err = CalculateQuote(opts, 0, "monthly", map[string]string{"data_disk": "20"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 10 {
		t.Fatalf("数据盘 20G 期望 10，得到 %v", q.Total)
	}
}

func TestCalculateQuoteRangeUsesCyclePrice(t *testing.T) {
	opts := []repo.ConfigOption{{
		Field: "bw", Name: "带宽", Mode: "range", Min: 1, Max: 10, Step: 1,
		Subs: []repo.ConfigValue{{Min: 1, Max: 10, Pricing: map[string]float64{
			"monthly": 2, "quarterly": 5, "yearly": 18,
		}}},
	}}
	q, err := CalculateQuote(opts, 30, "quarterly", map[string]string{"bw": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 40 { // 30 + 2步×季付5元
		t.Fatalf("期望 40，得到 %v", q.Total)
	}
}

func TestCalculateQuoteRangeRejectsBelowMinimum(t *testing.T) {
	opts := []repo.ConfigOption{{
		Field: "cpu", Name: "CPU", Mode: "range", Min: 2, Max: 16, Step: 1,
		Subs: []repo.ConfigValue{{Min: 2, Max: 16, Pricing: map[string]float64{"monthly": 10}}},
	}}
	if _, err := CalculateQuote(opts, 0, "monthly", map[string]string{"cpu": "0"}); err == nil {
		t.Fatal("低于最小值应报错")
	}
}

func TestCalculateQuoteRejectInjection(t *testing.T) {
	opts := []repo.ConfigOption{}
	// 未声明的字段一律忽略，不影响价格
	q, err := CalculateQuote(opts, 30, "monthly", map[string]string{"hack": "-999"})
	if err != nil || q.Total != 30 {
		t.Fatalf("未知字段应被忽略: %v %v", q, err)
	}
}
