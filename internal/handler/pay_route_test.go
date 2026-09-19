package handler

import (
	"net/http"
	"testing"
)

// 路由注册不得 panic：Go 1.22 ServeMux 对"重叠但互不更具体"的通配路由会在注册时 panic
// （如 /a/{x}/b 与 /a/b/{y}），这类问题只会在进程启动时才暴露。
func TestPayRegisterRoutes(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Pay.Register 注册路由时 panic: %v", r)
		}
	}()
	(&Pay{}).Register(http.NewServeMux())
}
