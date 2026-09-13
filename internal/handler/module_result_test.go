package handler

import "testing"

func TestModuleResultJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
		msg  string
	}{
		{"json 成功", `{"status":200,"msg":"添加成功"}`, true, "添加成功"},
		{"json status 1000 视为成功", `{"status":1000}`, true, ""},
		{"json 失败", `{"status":405,"msg":"无权限"}`, false, "无权限"},
		{"json 无 status 视为成功", `{"data":{"id":1}}`, true, ""},
		{"json null 视为失败", `null`, false, "null"},
		{"非 json 文本视为失败", `upstream error`, false, "upstream error"},
		{"空响应视为失败", `  `, false, ""},
		{"长非 json 截断", `<html>` + string(make([]byte, 300)), false, "<html>" + string(make([]byte, 120-6))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := moduleResultJSON(c.raw)
			okv := out["ok"]
			gotOK := okv == true || okv == float64(1) || okv == int64(1) || okv == 1
			if gotOK != c.ok {
				t.Fatalf("ok = %v, want %v (out=%v)", okv, c.ok, out)
			}
			if c.msg != "" {
				if s, _ := out["msg"].(string); s != c.msg {
					t.Fatalf("msg = %q, want %q", s, c.msg)
				}
			}
		})
	}
}
