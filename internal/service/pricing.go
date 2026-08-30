package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"lumeidc/internal/money"

	"lumeidc/internal/repo"
)

// QuoteLine 单个配置项的计价明细。
type QuoteLine struct {
	Field string  `json:"field"`
	Name  string  `json:"name"`
	Value string  `json:"value"`
	Price float64 `json:"price"`
}

// Quote 服务端重算的报价结果。
type Quote struct {
	Base   float64     `json:"base"`
	Config []QuoteLine `json:"config"`
	Total  float64     `json:"total"`
}

// CalculateQuote 服务端权威计价：基础周期价 + Σ配置加价。
// 只认产品声明的配置项，用户提交的多余键一律丢弃（防注入/防篡改）。
func CalculateQuote(opts []repo.ConfigOption, basePrice float64, cycle string, selection map[string]string) (*Quote, error) {
	if !money.FiniteNonNegative(basePrice) {
		return nil, fmt.Errorf("基础价格无效")
	}
	q := &Quote{Base: basePrice, Total: basePrice}
	for _, opt := range opts {
		if opt.Hidden || opt.Field == "os" { // os 不参与金额
			continue
		}
		val := strings.TrimSpace(selection[opt.Field])
		if val == "" {
			if opt.Required {
				return nil, fmt.Errorf("请选择 %s", opt.Name)
			}
			continue
		}
		switch opt.Mode {
		case "range":
			line, err := priceRange(opt, val, cycle)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", opt.Name, err)
			}
			q.Config = append(q.Config, line)
			q.Total += line.Price
			if !money.FiniteNonNegative(q.Total) {
				return nil, fmt.Errorf("总价无效")
			}
		default: // select
			sub, ok := matchSub(opt, val)
			if !ok {
				return nil, fmt.Errorf("%s: 无效选项", opt.Name)
			}
			p := sub.Price(cycle)
			if !money.FiniteNonNegative(p) {
				return nil, fmt.Errorf("%s: 配置价格无效", opt.Name)
			}
			q.Config = append(q.Config, QuoteLine{Field: opt.Field, Name: opt.Name, Value: sub.Name, Price: p})
			q.Total += p
			if !money.FiniteNonNegative(q.Total) {
				return nil, fmt.Errorf("总价无效")
			}
		}
	}
	return q, nil
}

// priceRange 数量型计价：值落在某个子项区间 [min,max] 内，按该档单价计费。
// 魔方财务规则：
//   - 子项 qty_min>0（如 CPU 按核、带宽按 Mbps）＝单价 × 所选数量；
//   - 子项 qty_min==0（离散规格包，如 4G 内存、20G 磁盘）＝单价即该档总价。
//     超出所有区间报错（防止超大配置绕过定价）。
func priceRange(opt repo.ConfigOption, valStr, cycle string) (QuoteLine, error) {
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return QuoteLine{}, fmt.Errorf("数值无效")
	}
	if v < 0 || (opt.Min > 0 && v < opt.Min) {
		return QuoteLine{}, fmt.Errorf("数值不能小于最小值 %g", opt.Min)
	}
	if v == 0 {
		return QuoteLine{
			Field: opt.Field, Name: opt.Name,
			Value: fmt.Sprintf("0%s", opt.Unit),
		}, nil
	}
	if len(opt.Subs) == 0 {
		return QuoteLine{}, fmt.Errorf("未配置价格")
	}
	subs := append([]repo.ConfigValue(nil), opt.Subs...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Min < subs[j].Min })
	// 跨度区间子项（qmax>0，如数据盘 "0|0G" 0~120GB）：单价 × 数量；
	// 离散规格包（qmax==0，如 "20|20G"）：按名称解析规格，仅当所选数量==规格时命中（单价即总价）。
	var exact *repo.ConfigValue
	var rangeSub *repo.ConfigValue
	for i := range subs {
		s := &subs[i]
		maxB := s.Max
		if maxB == 0 {
			maxB = opt.Max
		}
		if v < s.Min || v > maxB {
			continue
		}
		if s.Max > s.Min {
			if rangeSub == nil {
				rangeSub = s
			}
		} else if subSize(*s) == v {
			exact = s
			break
		}
	}
	matched := exact
	if matched == nil {
		matched = rangeSub
	}
	if matched == nil {
		return QuoteLine{}, fmt.Errorf("超出可选范围 %g~%g", opt.Min, opt.Max)
	}
	step := opt.Step
	if step <= 0 {
		step = 1
	}
	unitPrice := matched.Price(cycle)
	var price float64
	if matched.Max > matched.Min {
		price = unitPrice * math.Floor(v/step)
	} else {
		price = unitPrice
	}
	return QuoteLine{
		Field: opt.Field, Name: opt.Name,
		Value: fmt.Sprintf("%g%s", v, opt.Unit),
		Price: math.Round(price*100) / 100,
	}, nil
}

// subSize 从离散规格包的名称/值中解析其规格数值（如 "20G"、"20|20G" → 20）。
func subSize(s repo.ConfigValue) float64 {
	for _, str := range []string{s.Value, s.Name} {
		if n, ok := leadNum(str); ok {
			return n
		}
	}
	return s.Min
}

func leadNum(str string) (float64, bool) {
	var b strings.Builder
	for _, r := range str {
		if (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			break
		}
	}
	if b.Len() == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func matchSub(opt repo.ConfigOption, val string) (repo.ConfigValue, bool) {
	for _, sub := range opt.Subs {
		v := sub.Value
		if v == "" {
			v = sub.Name
		}
		if v == val || sub.Name == val {
			return sub, true
		}
	}
	return repo.ConfigValue{}, false
}

// DisplayPrice 目录展示月价：（基础价 + 最低一档配置价）×(1+利润比例%) 或 +固定利润。
// 无配置项的产品退化为仅基础价。纯展示用，绝不参与真实计价（真实计价在 CreateOrder）。
// ponytail: 配置型产品基础价只是“裸产品价”，必须叠加最低可选配置（CPU/内存等）才是真实起步价。
func DisplayPrice(base float64, opts []repo.ConfigOption, profitType int16, profitValue float64) float64 {
	var cfgTotal float64
	for _, o := range opts {
		if o.Hidden || o.Field == "os" { // 同 CalculateQuote 口径：os 不计价
			continue
		}
		if o.Mode == "range" {
			if o.Min <= 0 {
				continue // 可配 0 = 免费
			}
			// 数量型最低一档：单价 × 最小数量（与 priceRange 口径一致）
			v := o.Min
			for _, s := range o.Subs {
				maxB := s.Max
				if maxB == 0 {
					maxB = o.Max
				}
				if v >= s.Min && v <= maxB {
					step := o.Step
					if step <= 0 {
						step = 1
					}
					unit := s.Price("")
					if s.Max > s.Min {
						cfgTotal += unit * math.Floor(v/step)
					} else {
						cfgTotal += unit
					}
					break
				}
			}
			continue
		}
		best := -1.0
		for _, s := range o.Subs {
			p := s.Price("")
			if p < 0 {
				p = 0
			}
			if best < 0 || p < best {
				best = p
			}
		}
		if best > 0 {
			cfgTotal += best
		}
	}
	return math.Round(applyProfit(base+cfgTotal, profitType, profitValue)*100) / 100
}
