package handler

import (
	"encoding/json"
	"net/http"
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
