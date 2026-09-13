package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	moneypkg "lumeidc/internal/money"
)

// ConfigOption 产品可配置项。
type ConfigOption struct {
	Field    string        `json:"field"`       // 配置标识：cpu/memory/disk/bw/os…
	Name     string        `json:"name"`        // 显示名
	Mode     string        `json:"option_mode"` // select 单选 / range 数量范围
	Min      float64       `json:"min"`
	Max      float64       `json:"max"`
	Step     float64       `json:"step"`           // range 步长（每步加价单位）
	Unit     string        `json:"unit,omitempty"` // 后缀：核/GB/Mbps
	Required bool          `json:"required,omitempty"`
	Sort     int           `json:"sort_order,omitempty"`
	Hidden   bool          `json:"hidden,omitempty"`
	Subs     []ConfigValue `json:"sub,omitempty"` // select 型子项
}

// ConfigValue select 子项：显示名 + 各周期固定加价 + 可选阶梯区间。
type ConfigValue struct {
	Name    string             `json:"name"`              // 显示名，如 "2核"
	Value   string             `json:"value,omitempty"`   // 提交值，缺省用 Name
	Min     float64            `json:"min"`               // range 阶梯区间下界
	Max     float64            `json:"max"`               // range 阶梯区间上界
	Pricing map[string]float64 `json:"pricing,omitempty"` // monthly/quarterly/yearly 加价
	// Setup 各周期初装费（上游 setupfee，一次性）：只在首次购买收取，续费不收。
	// 上游对季/年付有独立的 qsetupfee/asetupfee，同步时三周期键都写（含 0）。
	Setup map[string]float64 `json:"setup,omitempty"`
}

// SetupPrice 取该子项在指定周期的初装费。
// 与 Price 不同，这里**不做跨周期回落**：上游按周期分别配置初装费，
// 缺失就是该周期不收，回落会把月付的初装费加到季/年付上（凭空多收）。
func (v ConfigValue) SetupPrice(cycle string) float64 {
	if v.Setup == nil {
		return 0
	}
	if cycle == "yearly" {
		if p, ok := v.Setup["yearly"]; ok {
			return p
		}
		return v.Setup["annually"]
	}
	return v.Setup[cycle]
}

// Price returns the surcharge for a billing cycle. Legacy "annually" data is
// normalized for the local "yearly" cycle; a missing quarterly/yearly price
// falls back to the monthly price, keeping the backend quote identical to the
// frontend priceOf() display. (Previously it returned 0, which let quarterly/
// yearly orders get every config surcharge for free and charged less than the
// price shown on the buy page.)
func (v ConfigValue) Price(cycle string) float64 {
	if v.Pricing == nil {
		return 0
	}
	if cycle == "yearly" {
		if p, ok := v.Pricing["yearly"]; ok {
			return p
		}
		if p, ok := v.Pricing["annually"]; ok {
			return p
		}
	} else if p, ok := v.Pricing[cycle]; ok {
		return p
	}
	return v.Pricing["monthly"]
}

// GetConfigOptions reads product configoption JSON. Returns empty slice when unset.
func (p *Products) GetConfigOptions(ctx context.Context, productID int64) ([]ConfigOption, error) {
	var raw []byte
	err := p.db.QueryRowContext(ctx,
		`SELECT configoption FROM products WHERE id=$1`, productID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []ConfigOption{}, nil
	}
	var out []ConfigOption
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ConfigOptionsByProducts 批量读取产品配置选项 JSON（列表页批量计价用）。
// 缺失或解析失败的产品不在结果中，调用方按空选项处理（与逐条 GetConfigOptions 出错语义一致）。
func (p *Products) ConfigOptionsByProducts(ctx context.Context, ids []int64) map[int64][]ConfigOption {
	out := make(map[int64][]ConfigOption, len(ids))
	if len(ids) == 0 {
		return out
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, configoption FROM products WHERE id = ANY($1)`, ids)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		if len(raw) == 0 {
			out[id] = []ConfigOption{}
			continue
		}
		var opts []ConfigOption
		if json.Unmarshal(raw, &opts) == nil {
			out[id] = opts
		}
	}
	return out
}

// SaveConfigOptions writes product configoption JSON.
func (p *Products) SaveConfigOptions(ctx context.Context, productID int64, opts []ConfigOption) error {
	for _, opt := range opts {
		if !moneypkg.FiniteNonNegative(opt.Min) || !moneypkg.FiniteNonNegative(opt.Max) || !moneypkg.FiniteNonNegative(opt.Step) {
			return errors.New("配置范围或步长无效")
		}
		for _, sub := range opt.Subs {
			if !moneypkg.FiniteNonNegative(sub.Min) || !moneypkg.FiniteNonNegative(sub.Max) {
				return errors.New("配置档位范围无效")
			}
			for _, v := range sub.Pricing {
				if !moneypkg.FiniteNonNegative(v) {
					return errors.New("配置价格无效")
				}
			}
			for _, v := range sub.Setup {
				if !moneypkg.FiniteNonNegative(v) {
					return errors.New("配置初装费无效")
				}
			}
		}
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx,
		`UPDATE products SET configoption=$2 WHERE id=$1`, productID, b)
	return err
}

var _ = sql.ErrNoRows
