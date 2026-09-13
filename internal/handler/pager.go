package handler

import (
	"net/http"
	"strconv"
)

// pageParam 读取 ?p= 页码，非法/越界时钳制到 [1, 1e6]。
func pageParam(r *http.Request) int {
	raw := r.URL.Query().Get("p")
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	if n > 1000000 {
		return 1000000
	}
	return n
}
