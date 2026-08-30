package handler

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"text/template"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

//go:embed templates/auth.html
var authFS embed.FS

type Auth struct {
	Users    *repo.Users
	Sessions *middleware.Store
	Lockout  *repo.LoginAttempts
	Notifier *service.Notifier
}

func (h *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /register", h.registerForm)
	mux.HandleFunc("POST /register", h.registerSubmit)
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /verify", h.verifyEmail)
}

func renderAuth(w http.ResponseWriter, data map[string]any) {
	data["SiteName"] = "LumeIDC"
	tpl, err := template.ParseFS(authFS, "templates/auth.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tpl.Execute(w, data); err != nil {
		// 模板执行错误已写入响应，无法再修改 HTTP 状态码，仅记录
		log.Printf("[template] auth.html 执行失败: %v", err)
	}
}

func (h *Auth) registerForm(w http.ResponseWriter, r *http.Request) {
	renderAuth(w, map[string]any{"IsRegister": true, "CSRF": csrfOf(h.Sessions, w, r)})
}

func (h *Auth) registerSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	pass := r.PostFormValue("password")
	if email == "" || len(pass) < 8 {
		renderAuth(w, map[string]any{"IsRegister": true, "Error": "邮箱必填，密码至少 8 位"})
		return
	}
	if _, e := mail.ParseAddress(email); e != nil {
		renderAuth(w, map[string]any{"IsRegister": true, "Error": "邮箱格式不正确"})
		return
	}
	id, err := h.Users.Create(r.Context(), email, pass, r.PostFormValue("name"))
	if err != nil {
		renderAuth(w, map[string]any{"IsRegister": true, "Error": "注册失败：邮箱可能已被占用"})
		return
	}
	// 邮箱验证：配置了 SMTP 则发验证邮件并等待验证；否则直接放行，保证无邮件能力时仍可注册。
	if h.Notifier != nil {
		tok := genToken()
		if e := h.Users.SetVerifyToken(r.Context(), id, tok); e == nil {
			link := "https://" + r.Host + "/verify?token=" + tok
			if se := h.Notifier.SendMail(r.Context(), email, "请验证邮箱", "点击完成注册验证："+link); se != nil {
				log.Printf("[register] 验证邮件发送失败，直接放行: %v", se)
				_ = h.Users.MarkVerified(r.Context(), id)
			}
		}
	} else {
		_ = h.Users.MarkVerified(r.Context(), id)
	}
	sess := h.Sessions.Start(w)
	sess.UserID = id
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// verifyEmail GET /verify?token=... — 完成邮箱验证。
func (h *Auth) verifyEmail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tok := r.URL.Query().Get("token")
	var msg string
	switch {
	case tok == "":
		msg = "缺少验证参数"
	case h.Users.VerifyEmail(r.Context(), tok) != nil:
		msg = "验证失败：链接无效或已使用"
	default:
		msg = "邮箱验证成功，请前往登录。"
	}
	fmt.Fprintf(w, verifyPageHTML, msg)
}

func (h *Auth) loginForm(w http.ResponseWriter, r *http.Request) {
	renderAuth(w, map[string]any{"CSRF": csrfOf(h.Sessions, w, r), "Next": safeNext(r.URL.Query().Get("next"))})
}

func (h *Auth) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	// 登录锁定：达阈值后临时拒绝，阻断爆破。
	if h.Lockout != nil {
		if locked, lerr := h.Lockout.Locked(r.Context(), email); lerr == nil && locked {
			w.WriteHeader(http.StatusTooManyRequests)
			renderAuth(w, map[string]any{"Error": "尝试次数过多，账户已临时锁定，请 15 分钟后再试"})
			return
		}
	}
	user, hash, err := h.Users.ByEmail(r.Context(), email)
	if err != nil || !h.Users.VerifyPassword(hash, r.PostFormValue("password")) {
		if h.Lockout != nil {
			_ = h.Lockout.Fail(r.Context(), email)
		}
		time.Sleep(300 * time.Millisecond) // 登录限速：简单恒定延迟
		w.WriteHeader(http.StatusUnauthorized)
		renderAuth(w, map[string]any{"Error": "邮箱或密码错误"})
		return
	}
	if !user.Verified {
		w.WriteHeader(http.StatusUnauthorized)
		renderAuth(w, map[string]any{"Error": "请先完成邮箱验证（验证邮件已发送，未收到可联系管理员）"})
		return
	}
	if h.Lockout != nil {
		_ = h.Lockout.Clear(r.Context(), email)
	}
	sess := h.Sessions.Start(w)
	sess.UserID = user.ID
	http.Redirect(w, r, safeNext(r.PostFormValue("next")), http.StatusSeeOther)
}

// safeNext 仅允许站内相对路径，防开放重定向。
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") || strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}

func (h *Auth) logout(w http.ResponseWriter, r *http.Request) {
	h.Sessions.Destroy(r, w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// csrfOf lazily creates an anonymous session to carry a CSRF token.
func csrfOf(s *middleware.Store, w http.ResponseWriter, r *http.Request) string {
	if sess := middleware.FromSession(r.Context()); sess != nil {
		return sess.CSRFToken()
	}
	ns := s.Start(w)
	ctx := middleware.WithSession(r.Context(), ns)
	*r = *r.WithContext(ctx)
	return ns.CSRFToken()
}

// genToken 生成 32 字符十六进制随机令牌（邮箱验证等）。
func genToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

const verifyPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>邮箱验证</title><style>body{font-family:system-ui,sans-serif;max-width:420px;margin:80px auto;text-align:center;color:#1a1a2e}</style></head>
<body><h1>LumeIDC</h1><p>%s</p><p><a href="/login">前往登录</a></p></body></html>`
