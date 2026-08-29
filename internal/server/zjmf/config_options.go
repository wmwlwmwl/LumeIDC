package zjmf

import (
	"context"
	"strconv"
	"strings"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// ZJMF option_type 数字 → 语义字段。
var typeFieldMap = map[int]string{
	4: "ip_num", 5: "os", 6: "cpu", 7: "cpu", 8: "memory", 9: "memory",
	10: "bw", 11: "bw", 12: "area", 13: "disk", 14: "disk",
	15: "flow", 16: "gpu", 17: "snapshot", 18: "backup", 19: "disk",
}

// rangeTypes 滑条数值型（其余为枚举单选）。
var rangeTypes = map[int]bool{4: true, 7: true, 9: true, 11: true, 14: true, 15: true, 16: true, 17: true, 18: true, 19: true}

// zjmfConfigResp 真实 ZJMF 响应：配置项在 data.config_groups[].options[] 或 data.config_options[]。
// 魔方财务（mf_cloud 等）实际返回 config_options 数组（field/option_mode/sub[].pricing 单数）。
type zjmfConfigResp struct {
	Data struct {
		ConfigGroups []struct {
			Name    string           `json:"name"`
			Options []map[string]any `json:"options"`
		} `json:"config_groups"`
		ConfigOptions []upstreamConfigOption `json:"config_options"`
	} `json:"data"`
}

// upstreamConfigOption 魔方财务实际下发的 config_options 元素：
// field=语义字段，option_mode=select/range，sub[].pricing 为单数对象（monthly 等）。
type upstreamConfigOption struct {
	Field      string  `json:"field"`
	Name       string  `json:"name"`
	OptionMode string  `json:"option_mode"`
	Required   bool    `json:"required"`
	Hidden     bool    `json:"hidden"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	Step       float64 `json:"step"`
	Sub        []struct {
		Name    string  `json:"name"`
		Value   string  `json:"value"`
		Min     float64 `json:"min"`
		Max     float64 `json:"max"`
		Pricing struct {
			Monthly   float64 `json:"monthly"`
			Quarterly float64 `json:"quarterly"`
			Annually  float64 `json:"annually"`
		} `json:"pricing"`
	} `json:"sub"`
}

// FetchProductConfigOptions 拉取上游商品配置项并转换为本地 ConfigOption 结构。
func (p Provider) FetchProductConfigOptions(ctx context.Context, cfg server.Config, upstreamPID int64) ([]repo.ConfigOption, error) {
	var raw zjmfConfigResp
	if err := getJSON(ctx, cfg,
		"/cart/get_product_config?pid="+strconv.FormatInt(upstreamPID, 10), &raw); err != nil {
		return nil, err
	}
	var opts []repo.ConfigOption
	if len(raw.Data.ConfigOptions) > 0 {
		for _, item := range raw.Data.ConfigOptions {
			opts = append(opts, convertUpstreamConfigOption(item))
		}
	} else {
		for _, g := range raw.Data.ConfigGroups {
			opts = append(opts, convertConfigOptions(g.Options)...)
		}
	}
	return opts, nil
}

// convertUpstreamConfigOption 解析魔方财务 config_options 格式（field/option_mode/sub[].pricing）。
// range 档按数量×单价计价；sub[].min/max 用于阶梯区间。
func convertUpstreamConfigOption(item upstreamConfigOption) repo.ConfigOption {
	field := item.Field
	if field == "" {
		field = "opt"
	}
	name := item.Name
	if name == "" {
		name = field
	}
	mode := "select"
	if strings.EqualFold(item.OptionMode, "range") {
		mode = "range"
	}
	opt := repo.ConfigOption{
		Field: field, Name: name,
		Required: item.Required, Hidden: item.Hidden,
		Mode: mode,
		Min:  item.Min, Max: item.Max,
		Step: item.Step,
	}
	for _, sm := range item.Sub {
		pricing := map[string]float64{}
		if sm.Pricing.Monthly > 0 {
			pricing["monthly"] = sm.Pricing.Monthly
		}
		if sm.Pricing.Quarterly > 0 {
			pricing["quarterly"] = sm.Pricing.Quarterly
		}
		if sm.Pricing.Annually > 0 {
			pricing["yearly"] = sm.Pricing.Annually
		}
		cv := repo.ConfigValue{Name: sm.Name, Value: sm.Value, Pricing: pricing}
		if sm.Min > 0 || sm.Max > 0 {
			cv.Min, cv.Max = sm.Min, sm.Max
		}
		opt.Subs = append(opt.Subs, cv)
	}
	return opt
}

// convertConfigOptions 解析 config_groups[].options[]（真实 ZJMF 结构）。
// option_name: "field|显示名"；sub[].option_name: "value|label"（^ 为分层分隔）；
// 价格在 sub[].pricings[0] 的 monthly/quarterly/annually 键，-1 表示未开启。
func convertConfigOptions(items []map[string]any) []repo.ConfigOption {
	var out []repo.ConfigOption
	for _, item := range items {
		rawOptName := str(item["option_name"], item["name"])
		field := typeFieldMap[int(num(item["option_type"]))]
		name := rawOptName
		if i := strings.Index(rawOptName, "|"); i >= 0 {
			field = strings.ToLower(strings.TrimSpace(rawOptName[:i]))
			name = strings.TrimSpace(rawOptName[i+1:])
		}
		if field == "" {
			field = "opt" + strconv.Itoa(len(out)+1)
		}
		if name == "" {
			name = field
		}
		optType := int(num(item["option_type"]))
		opt := repo.ConfigOption{
			Field: field, Name: name,
			Required: boolVal(item["required"]),
			Hidden:   boolVal(item["hidden"]),
		}
		subsRaw, _ := item["sub"].([]any)
		switch {
		case optType == 5: // 操作系统：不计价，仅选择
			opt.Mode = "select"
			for _, sm := range asMaps(subsRaw) {
				label, val := splitSubName(str(sm["option_name"]))
				opt.Subs = append(opt.Subs, repo.ConfigValue{Name: label, Value: val})
			}
		case rangeTypes[optType] || strings.EqualFold(str(item["option_mode"]), "range"):
			opt.Mode = "range"
			opt.Min = num(item["qty_minimum"])
			opt.Max = num(item["qty_maximum"])
			opt.Step = max(num(item["qty_stage"]), 1)
			opt.Unit = str(item["unit"])
			for _, sm := range asMaps(subsRaw) {
				pricing := extractPricings(sm)
				label, val := splitSubName(str(sm["option_name"]))
				cv := repo.ConfigValue{
					Name:    firstNonEmpty(label, name),
					Value:   val,
					Pricing: pricing,
				}
				// 阶梯区间（数量范围型子项）
				if mn, mx := num(sm["qty_minimum"]), num(sm["qty_maximum"]); mn > 0 || mx > 0 {
					cv.Min, cv.Max = mn, mx
				}
				opt.Subs = append(opt.Subs, cv)
			}
			if len(opt.Subs) == 0 { // 无阶梯则整段一个价：从 option 自身 pricings 取
				opt.Subs = append(opt.Subs, repo.ConfigValue{
					Name: name, Min: opt.Min, Max: opt.Max,
					Pricing: optionPricing(item),
				})
			}
		default: // 单选
			opt.Mode = "select"
			for _, sm := range asMaps(subsRaw) {
				label, val := splitSubName(str(sm["option_name"]))
				if label == "" {
					continue
				}
				opt.Subs = append(opt.Subs, repo.ConfigValue{
					Name: label, Value: val,
					Pricing: extractPricings(sm),
				})
			}
		}
		out = append(out, opt)
	}
	return out
}

// splitSubName 拆分 "value|label"；无 | 时 value=label。^ 分层取最后一段为显示名。
func splitSubName(raw string) (label, value string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if i := strings.Index(raw, "|"); i >= 0 {
		value = raw[:i]
		label = raw[i+1:]
	} else {
		value, label = raw, raw
	}
	// ^ 分层："CN^陕西西安电信" → 显示"陕西西安电信"
	if j := strings.LastIndex(label, "^"); j >= 0 {
		label = label[j+1:]
	}
	return strings.TrimSpace(label), strings.TrimSpace(value)
}

// extractPricings 从 sub.pricings[0] 提取三周期加价。-1 视为未开启忽略。
func extractPricings(sm map[string]any) map[string]float64 {
	p := map[string]float64{}
	arr, _ := sm["pricings"].([]any)
	if len(arr) == 0 {
		return p
	}
	m, _ := arr[0].(map[string]any)
	if m == nil {
		return p
	}
	for _, cycle := range []string{"monthly", "quarterly", "annually"} {
		v := num(m[cycle])
		if v > 0 {
			key := cycle
			if cycle == "annually" {
				key = "yearly"
			}
			p[key] = v
		}
	}
	return p
}

// optionPricing 从 option 自身 pricings[0] 提取（整段计价的范围型）。
func optionPricing(item map[string]any) map[string]float64 {
	p := map[string]float64{}
	arr, _ := item["pricings"].([]any)
	if len(arr) == 0 {
		return p
	}
	m, _ := arr[0].(map[string]any)
	if m == nil {
		return p
	}
	for _, cycle := range []string{"monthly", "quarterly", "annually"} {
		v := num(m[cycle])
		if v > 0 {
			key := cycle
			if cycle == "annually" {
				key = "yearly"
			}
			p[key] = v
		}
	}
	return p
}

func asMaps(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
