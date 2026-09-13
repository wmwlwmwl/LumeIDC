package handler

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// jsonOK 输出 {ok:1, key:value} 成功响应（前端以 ok===1 判断）。
func jsonOK(w http.ResponseWriter, key string, v any) {
	writeJSON(w, map[string]any{"ok": 1, key: v})
}

// jsonFail 输出 {ok:0, msg:...} 失败响应（前端以 ok===0 判断）。
func jsonFail(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]any{"ok": 0, "msg": msg})
}

// jsonStatus 兼容“发送验证码/结构错误”这类既有 http.Error(status) 语义：
// SSR（浏览器导航）原样 http.Error；SPA（Accept: application/json）输出
// {ok: 状态<400} JSON，保留原 HTTP 状态码（如发送验证码 202）。
func jsonStatus(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if !wantsJSON(r) {
		http.Error(w, msg, status)
		return
	}
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"ok": status < 400, "msg": msg})
}

// wantsJSON 内容协商：客户端明确请求 application/json 且不接受 text/html 时返回 true。
// SPA 的 fetch 带 Accept: application/json（详见 web/src/http），浏览器导航仍取 HTML（SSR）。
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

// parsePostForm 解析表单/multipart 请求体（URL 编码解析失败时回退 multipart）。
func parsePostForm(r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		if mperr := r.ParseMultipartForm(12 << 20); mperr != nil {
			return err
		}
	}
	return nil
}

// jsonFloatString JSON 数字标量转字符串：整值去掉小数点，其余保留原精度。
func jsonFloatString(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// bodyValuesMulti 与 bodyValues 类似，但保留多值字段（如勾选列表 import[]）。
// JSON 数组 → 多个值；表单 → PostForm 的全部值。SPA 提交勾选列表时使用。
func bodyValuesMulti(r *http.Request) (map[string][]string, error) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := parsePostForm(r); err != nil {
			return nil, err
		}
		return r.PostForm, nil
	}
	var raw map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = []string{t}
		case bool:
			out[k] = []string{strconv.FormatBool(t)}
		case float64:
			out[k] = []string{jsonFloatString(t)}
		case []any:
			vals := make([]string, 0, len(t))
			for _, item := range t {
				switch it := item.(type) {
				case string:
					vals = append(vals, it)
				case float64:
					vals = append(vals, jsonFloatString(it))
				}
			}
			out[k] = vals
		}
	}
	return out, nil
}

// bodyValues 把请求体统一到 map[string]string，字段名与 SSR 表单契约一致：
// - application/json：解析 JSON 对象（SPA 提交），标量类型转字符串；
// - 其余（表单/multipart）：ParseForm/ParseMultipartForm 后取 PostForm 首值。
// 各 handler 调用一次，向后续逻辑传参，避免重复消费请求体。
func bodyValues(r *http.Request) (map[string]string, error) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := parsePostForm(r); err != nil {
			return nil, err
		}
		out := make(map[string]string, len(r.PostForm))
		for k, vs := range r.PostForm {
			if len(vs) > 0 {
				out[k] = vs[0]
			}
		}
		return out, nil
	}
	var raw map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = jsonFloatString(t)
		case bool:
			out[k] = strconv.FormatBool(t)
		}
	}
	return out, nil
}
