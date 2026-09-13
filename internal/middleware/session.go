package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/config"
)

// In-memory session store with HMAC-signed cookie. ponytail: 单进程内存 session，
// 重启即失效；多实例部署需换成 DB/Redis 存储（接口已抽象为 Store）。
type Store struct {
	mu   sync.RWMutex
	sess map[string]*Session
	key  []byte
	ttl  time.Duration
}

type Session struct {
	mu        sync.Mutex
	UserID    int64
	IsAdmin   bool
	CSRF      string
	Flash     string
	ExpiresAt time.Time
}

// SetFlash 设置一次性提示消息（如密码重置结果），读后即焚。
func (s *Session) SetFlash(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Flash = msg
}

// ConsumeFlash 读取并清除一次性提示消息，未设置返回空串。
func (s *Session) ConsumeFlash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.Flash
	s.Flash = ""
	return m
}

func (s *Session) CSRFToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CSRF == "" {
		s.CSRF = NewCSRFToken()
	}
	return s.CSRF
}

func NewStore(cfg *config.Config) (*Store, error) {
	key, err := base64.StdEncoding.DecodeString(cfg.SecretKey)
	if err != nil || len(key) < 32 {
		return nil, errSecretKey
	}
	s := &Store{sess: map[string]*Session{}, key: key, ttl: 24 * 7 * time.Hour}
	go s.gcLoop()
	return s, nil
}

// gcLoop 周期清理过期会话：此前过期项仅在再次访问时删除，不回访的会话会永久驻留内存。
// ponytail: O(n) 全扫，当前会话量级完全够用；会话数显著增大时再换分片或堆结构。
func (s *Store) gcLoop() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for id, sess := range s.sess {
			if now.After(sess.ExpiresAt) {
				delete(s.sess, id)
			}
		}
		s.mu.Unlock()
	}
}

var errSecretKey = errInvalid("secret_key 无效：必须是 base64 编码的至少 32 字节")

type errInvalid string

func (e errInvalid) Error() string { return string(e) }

func (s *Store) newToken() (string, *Session) {
	raw := make([]byte, 32)
	rand.Read(raw)
	id := base64.RawURLEncoding.EncodeToString(raw)
	mac := s.sign(id)
	sess := &Session{CSRF: NewCSRFToken(), ExpiresAt: time.Now().Add(s.ttl)}
	return id + "." + mac, sess
}

func (s *Store) sign(id string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// 登录通道各自持有一份 cookie：管理员与普通用户在同一浏览器可同时登录，互不顶替。
// 以不同 cookie 名隔离，与魔方财务的做法一致。
const (
	userCookieName  = "lume_session"
	adminCookieName = "lume_admin_session"
)

// GetUser 读取普通用户通道会话（含未登录的匿名 CSRF 会话）。
func (s *Store) GetUser(r *http.Request) *Session { return s.get(r, userCookieName, false) }

// GetAdmin 读取管理员通道会话。
func (s *Store) GetAdmin(r *http.Request) *Session { return s.get(r, adminCookieName, true) }

// get 校验 cookie 签名并取回会话；admin 为期望通道，用于拒绝跨通道串用
// （例如把管理员 cookie 的值塞进用户 cookie 名）。
func (s *Store) get(r *http.Request, name string, admin bool) *Session {
	c, err := r.Cookie(name)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 || !hmac.Equal([]byte(s.sign(parts[0])), []byte(parts[1])) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sess[parts[0]]
	if sess == nil {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sess, parts[0])
		return nil
	}
	if sess.IsAdmin != admin {
		return nil // 通道不符：不认，但不动另一通道的会话
	}
	return sess
}

// isAdminRequest 判定后台通道请求：内部 /admin 及其子路径。
// 自定义后台路径已在会话中间件之前被 AdminPath 改写为 /admin/*，故此处可统一判断。
func isAdminRequest(r *http.Request) bool {
	p := r.URL.Path
	return p == "/admin" || strings.HasPrefix(p, "/admin/")
}

// isSecureRequest 按当前请求判断是否 HTTPS 通道（TLS 直连或反向代理透传标记）。
func (s *Store) isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// Start 启动普通用户通道会话（登录/注册与匿名 CSRF 会话共用；不影响管理员通道）。
func (s *Store) Start(r *http.Request, w http.ResponseWriter) *Session {
	return s.start(r, w, userCookieName, false)
}

// StartAdmin 启动管理员通道会话（与普通用户会话可并存）。
func (s *Store) StartAdmin(r *http.Request, w http.ResponseWriter) *Session {
	return s.start(r, w, adminCookieName, true)
}

func (s *Store) start(r *http.Request, w http.ResponseWriter, name string, admin bool) *Session {
	token, sess := s.newToken()
	sess.IsAdmin = admin
	id := strings.SplitN(token, ".", 2)[0]
	s.mu.Lock()
	s.sess[id] = sess
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.isSecureRequest(r),
	})
	return sess
}

// RevokeUser 删除指定普通用户的全部会话。
func (s *Store) RevokeUser(userID int64) {
	s.revoke(userID, false)
}

// RevokeAdmin 删除指定管理员的全部会话。
func (s *Store) RevokeAdmin(adminID int64) {
	s.revoke(adminID, true)
}

func (s *Store) revoke(userID int64, admin bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sess {
		if sess.UserID == userID && sess.IsAdmin == admin {
			delete(s.sess, id)
		}
	}
}

// Destroy 清除普通用户通道会话（用户登出）。
func (s *Store) Destroy(r *http.Request, w http.ResponseWriter) {
	s.destroy(r, w, userCookieName)
}

// DestroyAdmin 清除管理员通道会话（管理员登出）。
func (s *Store) DestroyAdmin(r *http.Request, w http.ResponseWriter) {
	s.destroy(r, w, adminCookieName)
}

func (s *Store) destroy(r *http.Request, w http.ResponseWriter, name string) {
	if c, err := r.Cookie(name); err == nil {
		id := strings.SplitN(c.Value, ".", 2)[0]
		s.mu.Lock()
		delete(s.sess, id)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

type ctxKey int

const sessionKey ctxKey = 0

// Middleware 按请求路径把两个通道的会话挂进上下文：后台请求（/admin/*）用管理员通道，
// 其余用普通用户通道；另一通道会话一并挂载，供后台登录前（管理员通道尚未建立）
// 的 CSRF 校验兜底。
func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, admin := s.GetUser(r), s.GetAdmin(r)
		cur, other := user, admin
		if isAdminRequest(r) {
			cur, other = admin, user
		}
		ctx := WithSession(r.Context(), cur)
		ctx = withOtherSession(ctx, other)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
