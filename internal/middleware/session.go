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
	mu     sync.RWMutex
	sess   map[string]*Session
	key    []byte
	ttl    time.Duration
	secure bool // 是否在 Set-Cookie 时附加 Secure（HTTPS 部署应为 true）
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
	// ponytail: 仅当 BaseURL 为 https 时才置 Secure；纯 http 本地开发置 false，否则浏览器拒收 cookie。
	return &Store{sess: map[string]*Session{}, key: key, ttl: 24 * 7 * time.Hour,
		secure: strings.HasPrefix(strings.ToLower(cfg.BaseURL), "https://")}, nil
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

const cookieName = "lume_session"

func (s *Store) Get(r *http.Request) *Session {
	c, err := r.Cookie(cookieName)
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
	if sess == nil || time.Now().After(sess.ExpiresAt) {
		if sess != nil {
			delete(s.sess, parts[0])
		}
		return nil
	}
	return sess
}

func (s *Store) Start(w http.ResponseWriter) *Session {
	token, sess := s.newToken()
	id := strings.SplitN(token, ".", 2)[0]
	s.mu.Lock()
	s.sess[id] = sess
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure,
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
func (s *Store) Destroy(r *http.Request, w http.ResponseWriter) {
	if c, err := r.Cookie(cookieName); err == nil {
		id := strings.SplitN(c.Value, ".", 2)[0]
		s.mu.Lock()
		delete(s.sess, id)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

type ctxKey int

const sessionKey ctxKey = 0

func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithSession(r.Context(), s.Get(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
