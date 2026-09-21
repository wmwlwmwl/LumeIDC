package csdk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxResponseBytes 校验接口响应上限（64KB，超出视为无效响应）。
const MaxResponseBytes = 64 << 10

// SettingsGetter 设置读取（repo.Settings 满足该接口；定义在此避免 csdk 依赖 repo）。
type SettingsGetter interface {
	Get(ctx context.Context, key string) (string, error)
}

// Host 适配器宿主：设置读取 + HTTP 传输。
// Client 为 nil 时兜底默认客户端（10s 超时）。
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

// HTTPClient 返回注入的客户端；nil 时兜底 10s 超时默认客户端。
// 各家校验请求形态差异大（表单/JSON/Bearer），适配器据此自建请求。
func (h *Host) HTTPClient() *http.Client {
	if h != nil && h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ReadResponse 读取校验接口响应（限定 MaxResponseBytes，超出或读失败视为无效）。
func ReadResponse(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil || len(data) > MaxResponseBytes {
		return nil, errors.New("验证码响应无效")
	}
	return data, nil
}

// HexString 小写十六进制编码（签名用，避免适配器各自引入 encoding/hex 之外的实现）。
func HexString(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&15]
	}
	return string(out)
}
