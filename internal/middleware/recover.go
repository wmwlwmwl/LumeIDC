package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recover 捕获后续中间件与 handler 的 panic：记录堆栈后返回结构化 500。
//
// 没有它时 net/http 会直接断掉连接（客户端只看到"连接中断"），前端也拿不到
// 统一错误体，无法给出任何提示——同项目所有接口都按 {ok,msg} 约定返回。
// 必须包在最外层，才能覆盖内层中间件（会话/CSRF/后台路径）与全部 handler。
//
// 注意：若 panic 发生在响应已写出之后，客户端仍可能收到被截断的内容——
// 这是流式响应固有的限制，此处只保证「不再静默断开」。
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler 是 net/http 的正常控制流（如客户端提前断开、
			// 显式中断响应），不是故障，原样上抛交给标准库处理。
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			// 路径用 %q：它由客户端控制，直接 %s 打出换行可以在日志里伪造行。
			log.Printf("[recover] %s %q 处理时 panic: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
			http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
