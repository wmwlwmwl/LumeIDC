package service

import (
	"context"
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
	// Setup 该项初装费（上游一次性费用）：只在首次购买收取，续费不收。
	Setup float64 `json:"setup,omitempty"`
}

// Quote 服务端重算的报价结果。
type Quote struct {
	Base   float64     `json:"base"`
	Config []QuoteLine `json:"config"`
	// Setup 初装费合计（一次性）：仅首次购买计入应付；续费/升级不含。
	Setup float64 `json:"setup"`
	Total float64 `json:"total"`
}

// AvailableCycles 按月、季、年顺序返回有基础价的可售周期及默认周期。
func AvailableCycles(monthly, quarterly, yearly float64) (cycles []string, defaultCycle string) {
	for _, item := range []struct {
		name  string
		price float64
	}{
		{"monthly", monthly}, {"quarterly", quarterly}, {"yearly", yearly},
	} {
		if item.price > 0 {
			cycles = append(cycles, item.name)
		}
	}
	if len(cycles) > 0 {
		defaultCycle = cycles[0]
	}
	return cycles, defaultCycle
}

// PayableOnce 首次购买的成本基数 = 周期费 + 初装费。利润加成由调用方施加。
func (q *Quote) PayableOnce() float64 { return q.Total + q.Setup }

// CalculateQuote 服务端权威计价：基础周期价 + Σ配置加价（另汇总一次性初装费）。
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
			q.Setup += line.Setup
			if !money.FiniteNonNegative(q.Total) || !money.FiniteNonNegative(q.Setup) {
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
			s := sub.SetupPrice(cycle)
			if !money.FiniteNonNegative(s) {
				return nil, fmt.Errorf("%s: 初装费无效", opt.Name)
			}
			q.Config = append(q.Config, QuoteLine{Field: opt.Field, Name: opt.Name, Value: sub.Name, Price: p, Setup: s})
			q.Total += p
			q.Setup += s
			if !money.FiniteNonNegative(q.Total) || !money.FiniteNonNegative(q.Setup) {
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
		// 初装费按档位取一次，不随数量成倍（一次性费用，语义是开通/安装费）。
		Setup: matched.SetupPrice(cycle),
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

// MonthlySellPrice 某产品在给定配置选择下的"月售价"（基础月价+配置加价，按产品/服务器利润加成）。
// 升降级差价两侧统一用该口径：目标月等价 − 当前月等价。
func MonthlySellPrice(ctx context.Context, products *repo.Products, productID int64, selection map[string]string) (float64, error) {
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		return 0, err
	}
	pr, err := products.Price(ctx, productID, psID)
	if err != nil {
		return 0, err
	}
	base, err := strconv.ParseFloat(pr.Monthly, 64)
	if err != nil || !money.FiniteNonNegative(base) {
		return 0, fmt.Errorf("商品月价无效")
	}
	opts, err := products.GetConfigOptions(ctx, productID)
	if err != nil {
		return 0, err
	}
	quote, err := CalculateQuote(opts, base, "monthly", selection)
	if err != nil {
		return 0, err
	}
	pt, pv, err := products.ProductSellProfit(ctx, productID)
	if err != nil {
		return 0, err
	}
	return mathRound(applyProfit(mathRound(quote.Total), pt, pv)), nil
}

// cycleDays 各计费周期的折算天数，供升降级差价按剩余天数折算（proration）。
// 与到期/续费口径保持一致：月 30、季 90、年 365。
var cycleDays = map[string]float64{"monthly": 30, "quarterly": 90, "yearly": 365}

var cycleLabels = map[string]string{"monthly": "月付", "quarterly": "季付", "yearly": "年付"}

// CycleDays 周期折算天数；未知周期返回 0。
func CycleDays(cycle string) float64 { return cycleDays[cycle] }

// CycleSellPrice 某产品在给定周期与配置下的售价（基础周期价 + 配置加价，再按利润加成）。
// 与 MonthlySellPrice 同口径，区别在于周期可变——升级差价两侧可能处于不同周期（如月付→年付）。
// 季/年价未配置（<=0）时报错，避免把 0 元当作有效周期价算出差价为 0 的怪单。
func CycleSellPrice(ctx context.Context, products *repo.Products, productID int64, cycle string, selection map[string]string) (float64, error) {
	if _, ok := cycleDays[cycle]; !ok {
		return 0, fmt.Errorf("无效的计费周期: %s", cycle)
	}
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		return 0, err
	}
	pr, err := products.Price(ctx, productID, psID)
	if err != nil {
		return 0, err
	}
	raw := pr.Monthly
	if cycle == "quarterly" {
		raw = pr.Quarterly
	} else if cycle == "yearly" {
		raw = pr.Yearly
	}
	base, err := strconv.ParseFloat(raw, 64)
	if err != nil || base < 0 || (cycle != "monthly" && base <= 0) {
		return 0, fmt.Errorf("商品%s价格无效", cycleLabels[cycle])
	}
	opts, err := products.GetConfigOptions(ctx, productID)
	if err != nil {
		return 0, err
	}
	quote, err := CalculateQuote(opts, base, cycle, selection)
	if err != nil {
		return 0, err
	}
	pt, pv, err := products.ProductSellProfit(ctx, productID)
	if err != nil {
		return 0, err
	}
	return mathRound(applyProfit(mathRound(quote.Total), pt, pv)), nil
}

// ProratedDiff 按剩余天数折算的升降级差价：
//
//	diff = 目标周期价/目标周期天数 × R − 当前周期价/当前周期天数 × R
//
// 两侧各按自身周期折算日价，因此换周期（月付→年付）时金额与实际周期一致，
// 不会再出现"选年付却只收一个月差价"的错位。R 为剩余天数，向上取整（不足一天按一天算）。
// diff > 0 为升级补款，< 0 为降级（当前策略：不退款，见 prepareUpgrade）。
func ProratedDiff(curPrice float64, curCycle string, tgtPrice float64, tgtCycle string, remainDays float64) (float64, error) {
	cd, ok1 := cycleDays[curCycle]
	td, ok2 := cycleDays[tgtCycle]
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("无效的计费周期")
	}
	if remainDays <= 0 {
		return 0, fmt.Errorf("服务已到期，请先续费后再升降级")
	}
	return mathRound(tgtPrice/td*remainDays - curPrice/cd*remainDays), nil
}

// SellPriceFromData 内存版月售价：给定基础月价/配置选项/利润与选择，与 MonthlySellPrice 同口径。
// 供列表页批量计价（数据已随主查询带回，避免逐行 N 次查询）。
func SellPriceFromData(base float64, opts []repo.ConfigOption, pt int16, pv float64, selection map[string]string) float64 {
	quote, err := CalculateQuote(opts, base, "monthly", selection)
	if err != nil {
		return 0
	}
	return mathRound(applyProfit(mathRound(quote.Total), pt, pv))
}

// DisplayPrice 目录展示月价：（基础价 + 最低一档配置价）×(1+利润比例%) 或 +固定利润。
// 无配置项的产品退化为仅基础价。纯展示用，绝不参与真实计价（真实计价在 CreateOrder）。
// 不含一次性初装费——后台“月价”列与 0 元订单判定要的是周期价口径。
// ponytail: 配置型产品基础价只是“裸产品价”，必须叠加最低可选配置（CPU/内存等）才是真实起步价。
func DisplayPrice(base float64, opts []repo.ConfigOption, profitType int16, profitValue float64) float64 {
	return displayPrice(base, opts, profitType, profitValue, "monthly", false)
}

// DisplayStartPrice 门店起步价：周期价 + 最低配置价 + 最低配置档的初装费（一次性），再按利润加成。
// 对齐魔方财务商品列表口径（ViewModel：product_price = 周期价 + 初装费 + 最低配置价(含该项初装费)）：
// 上游 348 即 3 + 25 + 5 = ￥33.00，只看周期价会显示 28，比上游少。
// 用于商品列表与购买页周期标签。
func DisplayStartPrice(base float64, opts []repo.ConfigOption, profitType int16, profitValue float64, cycle string) float64 {
	return displayPrice(base, opts, profitType, profitValue, cycle, true)
}

func displayPrice(base float64, opts []repo.ConfigOption, profitType int16, profitValue float64, cycle string, withSetup bool) float64 {
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
					unit := s.Price(cycle)
					if s.Max > s.Min {
						cfgTotal += unit * math.Floor(v/step)
					} else {
						cfgTotal += unit
					}
					if withSetup {
						cfgTotal += s.SetupPrice(cycle) // 初装费按档位取一次，不随数量成倍
					}
					break
				}
			}
			continue
		}
		best := -1.0
		var bestSub repo.ConfigValue
		for _, s := range o.Subs {
			p := s.Price(cycle)
			if p < 0 {
				p = 0
			}
			if best < 0 || p < best {
				best, bestSub = p, s
			}
		}
		if best > 0 {
			cfgTotal += best
		}
		if withSetup {
			cfgTotal += bestSub.SetupPrice(cycle) // 最低档也可能是 0 元周期价 + 初装费
		}
	}
	return math.Round(applyProfit(base+cfgTotal, profitType, profitValue)*100) / 100
}
