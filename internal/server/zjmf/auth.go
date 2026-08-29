package zjmf

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/server"
)

// jwtCache 按规范化 APIURL + 用户名缓存 JWT，剩余有效期 <300s 时重登。
// 不同上游账号即使共用 API 地址也必须隔离 token。
var cache = &authCache{tokens: map[string]jwtEntry{}}

type authCache struct {
	mu     sync.Mutex
	tokens map[string]jwtEntry
}

type jwtEntry struct {
	token     string
	expiresAt time.Time
}

func authCacheKey(cfg server.Config) string {
	digest := sha256.Sum256([]byte(cfg.APIKey))
	return strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/") + "\x00" + cfg.APIUsername + "\x00" + hex.EncodeToString(digest[:]) + "\x00" + fmt.Sprintf("%d", cfg.CredentialRevision)
}

func (a *authCache) get(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.tokens[key]
	if !ok || time.Now().After(e.expiresAt.Add(-300*time.Second)) {
		return "", false
	}
	return e.token, true
}

func (a *authCache) put(key, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp := time.Now().Add(time.Hour) // 解不出 exp 时的兜底
	if p := parseJWTExp(token); p > 0 {
		exp = time.Unix(p, 0)
	}
	a.tokens[key] = jwtEntry{token: token, expiresAt: exp}
}

func (a *authCache) invalidate(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tokens, key)
}

func parseJWTExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(b, &payload) != nil {
		return 0
	}
	return payload.Exp
}

func login(ctx context.Context, cfg server.Config) (string, error) {
	form := url.Values{"username": {cfg.APIUsername}, "password": {cfg.APIKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.APIURL, "/")+"/zjmf_api_login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := defaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("上游登录失败: http %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("登录响应读取失败: %w", err)
	}
	var bodyJSON struct {
		JWT    string `json:"jwt"`
		Status int    `json:"status"`
		Msg    string `json:"msg"`
	}
	if err := json.Unmarshal(body, &bodyJSON); err != nil {
		return "", fmt.Errorf("登录响应解析失败: %w", err)
	}
	if bodyJSON.Status != 200 || bodyJSON.JWT == "" {
		return "", fmt.Errorf("上游登录失败: %s", bodyJSON.Msg)
	}
	return bodyJSON.JWT, nil
}

func ensureToken(ctx context.Context, cfg server.Config) (string, error) {
	key := authCacheKey(cfg)
	if t, ok := cache.get(key); ok {
		return t, nil
	}
	t, err := login(ctx, cfg)
	if err != nil {
		return "", err
	}
	cache.put(key, t)
	return t, nil
}
