package vsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SettingsGetter 设置读取（repo.Settings 满足该接口；定义在此避免 vsdk 依赖 repo）。
type SettingsGetter interface {
	Get(ctx context.Context, key string) (string, error)
}

// Host 适配器宿主：设置读取 + HTTP 传输 + 地址白名单。
// Client 为 nil 时兜底默认客户端（15s 超时）。
type Host struct {
	Settings SettingsGetter
	Client   *http.Client
}

// Get 读取设置值（去空白；宿主或设置源缺失时返回空串）。
func (h *Host) Get(ctx context.Context, key string) string {
	if h == nil || h.Settings == nil {
		return ""
	}
	v, _ := h.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

func (h *Host) httpClient() *http.Client {
	if h != nil && h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Endpoint 取实名服务地址：设置项优先、fallback 兜底；强制 https 且 host 须在白名单内。
func (h *Host) Endpoint(ctx context.Context, setting, fallback string, hosts ...string) (string, error) {
	raw := h.Get(ctx, setting)
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

// RequestJSON 发送请求（body 非空时带 JSON Content-Type），解析 JSON 响应到 dst。
func (h *Host) RequestJSON(ctx context.Context, method, endpoint string, body []byte, headers map[string]string, dst any) error {
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
	resp, err := h.httpClient().Do(req)
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

// RequestForm 发送 POST 表单，解析 JSON 响应到 dst。
func (h *Host) RequestForm(ctx context.Context, endpoint string, values url.Values, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return errors.New("实名服务请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.httpClient().Do(req)
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
