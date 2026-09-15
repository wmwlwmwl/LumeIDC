package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"lumeidc/internal/middleware"
)

//go:embed all:webui/dist
var webUIFS embed.FS

// WebUIBuilt 报告 Vite 构建产物是否存在（index.html / admin.html）。
// 生产运行要求前端已构建；缺失时组合根应拒绝启动并给出明确指引。
func WebUIBuilt() bool {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		return false
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return false
	}
	if _, err := fs.Stat(dist, "admin.html"); err != nil {
		return false
	}
	return true
}

// RegisterWebUI 挂载 Vite 构建产物（SPA）。
//
// 前端构建产物为强制依赖（启动时由 httpserver.Build 校验）。提供静态资源
// （/app/{file...}，详见 vite.config assetsDir）与 SPA 文档兜底（未匹配任何
// SSR/API 路由的 GET）。具体 SSR 路由、/assets/、/app/{file...} 比
// "GET /{path...}" 更精确，由 ServeMux 优先命中，因此不抢占保留的 SSR 页面。
//
// 若 dist 缺失（仅测试环境可能出现），则跳过注册；生产启动已在更早处拒绝。
func RegisterWebUI(mux *http.ServeMux) {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		return
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return // web/ 尚未构建，保持纯 SSR 现状
	}

	// Vite 资源（build.assetsDir = 'app'，产物位于 dist/app/，文件名带内容哈希，长缓存）。
	mux.Handle("GET /app/{file...}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if name == "" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFileFS(w, r, dist, "app/"+name)
	}))

	// 未匹配路径的 SPA 兜底（history 路由深链刷新）。具体 SSR/API 路由优先命中。
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// 安装向导：已安装系统不再提供该入口（装完即锁定），不应落到前台 SPA
		if p == "/install" || strings.HasPrefix(p, "/install/") {
			http.NotFound(w, r)
			return
		}
		// 后台路径下的未知深链交给后台壳（hash 路由自行兜底），避免显示前台 SPA
		doc := "index.html"
		if p == "/admin" || strings.HasPrefix(p, "/admin/") {
			doc = "admin.html"
		}
		serveSPADoc(w, r, dist, doc)
	})
}

// InstallPage 安装向导页：返回前台 SPA 外壳（/install）。
// 仅在未安装时由安装器的 mux 注册；SPA 内 Install.vue 负责表单与提交。
func InstallPage(w http.ResponseWriter, r *http.Request) {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		http.NotFound(w, r)
		return
	}
	serveSPADoc(w, r, dist, "index.html")
}

// spaOwned 判定该 GET 路径在「浏览器导航」时是否由 SPA 接管。
//
// 认证与实名页现已全部迁移到 SPA：/login、/register（含手机验证码流程）与
// /user/verification（含自动实名插件流程）恒由 SPA 承载。
func spaOwned(p string) bool {
	switch p {
	case "/", "/cart", "/services", "/notifications", "/tickets",
		"/user", "/user/recharge", "/user/invoices", "/user/password", "/user/profile",
		"/login", "/register", "/forgot", "/user/verification":
		return true
	}
	if strings.HasPrefix(p, "/buy/") {
		return true
	}
	if rest, ok := strings.CutPrefix(p, "/tickets/"); ok {
		return isAllDigits(rest)
	}
	// 服务详情 /services/{id}（纯数字）、升降级 /services/{id}/upgrade 与
	// VNC 控制台 /services/{id}/console 由 SPA 接管；
	// 其余子路径（module*(上游面板代理)、chart 等）仍走 SSR。
	if rest, ok := strings.CutPrefix(p, "/services/"); ok {
		if rest == "" {
			return false
		}
		if isAllDigits(rest) {
			return true
		}
		if seg, sub, found := strings.Cut(rest, "/"); found && isAllDigits(seg) &&
			(sub == "upgrade" || sub == "console") {
			return true
		}
		return false
	}
	if rest, ok := strings.CutPrefix(p, "/pay/"); ok {
		// 仅收银台 /pay/{id}（纯数字）由 SPA 接管；状态轮询 /pay/{id}/status、
		// 网关回调 /pay/notify、本地二维码页 /pay/qr 都保留给后端。
		return isAllDigits(rest)
	}
	return false
}

// isAllDigits 判定字符串为非空纯数字（服务 ID）。
func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// SPAGate 让 SPA 成为「已迁移路径」的实际界面：浏览器导航（Accept 非 JSON）
// 命中 spaOwned 时返回 SPA 外壳 index.html；API 请求（Accept: application/json）
// 与未迁移路径原样透传，SSR 行为不变。
//
// 背景：SSR 路由（如 GET /cart）比 SPA 的 "GET /{path...}" 兜底更精确，
// 若不加这一层，导航会一直命中旧 SSR 页面，SPA 永远进不去。
// 后台根路径（/admin，自定义后台路径经 AdminPath 改写后的目标）返回 admin.html。
//
// authReady / verifyReady 在每次请求时求值，为假则对应页面保持 SSR
// （/login、/register 与 /user/verification）。
// webui/dist 未构建时不包裹，保持纯 SSR。
func SPAGate(next http.Handler) http.Handler {
	dist, err := fs.Sub(webUIFS, "webui/dist")
	if err != nil {
		return next
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return next // 未构建前端：不接管任何路径
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			isPaymentPage := false
			if rest, ok := strings.CutPrefix(r.URL.Path, "/pay/"); ok && isAllDigits(rest) {
				isPaymentPage = r.URL.Query().Get("format") != "json" &&
					r.URL.Query().Get("out_trade_no") == ""
			}
			// /pay/{id} is a browser page. Its JSON data request carries an
			// explicit format=json marker, so unusual browser Accept headers
			// cannot make the raw API response appear in the address bar.
			if isPaymentPage {
				serveSPADoc(w, r, dist, "index.html")
				return
			}
			if !wantsJSON(r) {
				// 后台壳（含登录页）由后台 SPA 承载；SSR 登录页仅保留给非导航请求。
				if r.URL.Path == "/admin" || r.URL.Path == "/admin/" || r.URL.Path == "/admin/login" {
					serveSPADoc(w, r, dist, "admin.html")
					return
				}
				// 易支付页面回跳携带签名参数，必须交给后端 payPage 验签并核销；
				// 普通 /pay/{id} 导航仍由收银台 SPA 接管。
				isPaymentReturn := strings.HasPrefix(r.URL.Path, "/pay/") &&
					r.URL.Query().Get("out_trade_no") != ""
				if !isPaymentReturn && spaOwned(r.URL.Path) {
					serveSPADoc(w, r, dist, "index.html")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// serveSPADoc 下发 SPA 外壳。标记 NoRewrite：外壳中的资源名可能含 "admin"
// 字面量（如 Vite 产物 /app/admin-<hash>.js），被后台路径改写器改写会 404。
func serveSPADoc(w http.ResponseWriter, r *http.Request, dist fs.FS, doc string) {
	w.Header().Set(middleware.NoRewriteHeader, "1")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, dist, doc)
}
