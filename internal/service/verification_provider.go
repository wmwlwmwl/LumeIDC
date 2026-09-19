package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// VerificationProvider 是第三方实名服务的最小边界。provider 不得直接修改用户表。
type VerificationProvider interface {
	Key() string
	Start(context.Context, VerificationStartRequest) (VerificationStartResult, error)
	Poll(context.Context, string) (VerificationStatus, error)
}

type VerificationStartRequest struct {
	LegalName      string
	IdentityNumber string
	ReturnURL      string
}

type VerificationStartResult struct {
	ProviderRef string
	URL         string
}

type VerificationStatus struct {
	Status  string // pending, approved, rejected, failed
	Message string
}

// ConfiguredVerificationProvider 根据后台选择的 provider 创建受信任的内置适配器。
type ConfiguredVerificationProvider struct {
	Settings *repo.Settings
	BaseURL  string
	Client   *http.Client
}

func NewConfiguredVerificationProvider(settings *repo.Settings, baseURL string) *ConfiguredVerificationProvider {
	return &ConfiguredVerificationProvider{Settings: settings, BaseURL: strings.TrimRight(baseURL, "/"), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *ConfiguredVerificationProvider) get(ctx context.Context, key string) string {
	if f == nil || f.Settings == nil {
		return ""
	}
	v, _ := f.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

// Provider 统一走适配器注册表，不再有旧版 switch-case fallback。
func (f *ConfiguredVerificationProvider) Provider(ctx context.Context, key string) (VerificationProvider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	p, found, err := verificationFromRegistry(key, f)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("实名服务适配器未注册：" + key)
	}
	return p, nil
}

func (f *ConfiguredVerificationProvider) client() *http.Client {
	if f != nil && f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (f *ConfiguredVerificationProvider) endpoint(ctx context.Context, setting, fallback string, hosts ...string) (string, error) {
	raw := f.get(ctx, setting)
	if raw == "" {
		raw = fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return "", errors.New("实名服务地址不安全")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range hosts {
		if host == allowed {
			return strings.TrimRight(raw, "/"), nil
		}
	}
	return "", errors.New("实名服务地址不受支持")
}

func (f *ConfiguredVerificationProvider) requestJSON(ctx context.Context, method, endpoint string, body []byte, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("实名服务请求无效")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return errors.New("实名服务连接失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) == 0 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("实名服务请求失败")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("实名服务响应无效")
	}
	return nil
}

func (f *ConfiguredVerificationProvider) requestForm(ctx context.Context, endpoint string, values url.Values, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return errors.New("实名服务请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return errors.New("实名服务连接失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) == 0 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("实名服务请求失败")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("实名服务响应无效")
	}
	return nil
}

// -------- 适配器共用的工具函数 --------

func statusFromMap(m map[string]any) VerificationStatus {
	candidates := []string{"status", "verify_status", "auth_status", "conclusion", "verify_result"}
	var value string
	for _, key := range candidates {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case string:
				value = strings.ToLower(strings.TrimSpace(x))
			case float64:
				value = strconv.Itoa(int(x))
			case bool:
				if x {
					value = "true"
				}
			}
			if value != "" {
				break
			}
		}
	}
	if result, ok := m["result"].(map[string]any); ok && value == "" {
		return statusFromMap(result)
	}
	switch value {
	case "success", "passed", "pass", "approved", "1", "true":
		return VerificationStatus{Status: "approved"}
	case "failed", "failure", "rejected", "2", "-1", "false":
		return VerificationStatus{Status: "rejected"}
	default:
		return VerificationStatus{Status: "pending"}
	}
}

func responseOK(m map[string]any) bool {
	if m == nil {
		return false
	}
	known := false
	for _, key := range []string{"status", "code"} {
		if raw, exists := m[key]; exists {
			known = true
			switch v := raw.(type) {
			case float64:
				return v == 200 || v == 20000 || v == 1
			case string:
				return v == "200" || v == "20000" || strings.EqualFold(v, "ok") || v == "1"
			}
		}
	}
	if v, ok := m["success"].(bool); ok {
		return v
	}
	if known {
		return false
	}
	return false
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringValue(m, key); v != "" {
			return v
		}
	}
	return ""
}

func providerSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func providerRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
