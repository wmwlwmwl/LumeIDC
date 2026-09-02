package zjmf

import (
	"encoding/json"
	"strconv"
)

// extractCurrency 从商品配置响应里找 currencyid（结构因版本而异，宽松提取）。
func extractCurrency(pc map[string]any) string {
	if v, ok := pc["currencyid"]; ok {
		switch t := v.(type) {
		case float64:
			return strconvFormat(int64(t))
		case string:
			return t
		}
	}
	if data, ok := pc["data"].(map[string]any); ok {
		return extractCurrency(data)
	}
	return ""
}

func strconvFormat(n int64) string {
	return strconv.FormatInt(n, 10)
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings2TrimSpace(t), 64)
		return f
	}
	return 0
}

func strings2TrimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func str(vs ...any) string {
	for _, v := range vs {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return ""
}

func boolVal(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	}
	return false
}

// json.RawMessage 弱类型辅助。
func asArrayRaw(raw json.RawMessage) []map[string]any {
	var arr []map[string]any
	json.Unmarshal(raw, &arr)
	return arr
}

func rawNum(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return nil
}

func asArray(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}
