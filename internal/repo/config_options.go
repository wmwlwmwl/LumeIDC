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
	err := p.DB.QueryRowContext(ctx,
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
		}
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	_, err = p.DB.ExecContext(ctx,
		`UPDATE products SET configoption=$2 WHERE id=$1`, productID, b)
	return err
}

var _ = sql.ErrNoRows
