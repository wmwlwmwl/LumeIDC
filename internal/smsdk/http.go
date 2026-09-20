package smsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// UnknownError 发送结果未确认（防自动重发的安全错误类型——调用方不得据此重试）。
type UnknownError struct{}

func (UnknownError) Error() string { return "短信发送结果未确认，请勿自动重发" }

// DoRequest 所有短信适配器共用的 HTTP 传输层：请求构造、响应读取、
// 大小限制、BOM 去除。协议解析由各适配器自行处理。
// 强制不跟随重定向以避免 SSRF 和重试失控。client 为 nil 时兜底默认客户端
// （15s 超时）——路由/outbox 等无注入客户端的路径经此兜底。
func DoRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("短信请求无效")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := safeClient.Do(req)
	if e != nil {
		return nil, UnknownError{}
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, UnknownError{}
	}
	return bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), nil
}

// DecodeResponse 解析 JSON 响应（UseNumber 保精度；须恰好一个 JSON 值）。
func DecodeResponse(raw []byte) (map[string]any, error) {
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&out) != nil || out == nil || d.Decode(new(any)) != io.EOF {
		return nil, UnknownError{}
	}
	return out, nil
}

// Rejected 供应商明确拒绝的统一结果。
func Rejected() (ProviderResult, error) {
	return ProviderResult{ProviderCode: "provider_rejected", Message: "短信供应商明确拒绝请求"}, errors.New("短信供应商明确拒绝请求，请检查供应商控制台")
}
