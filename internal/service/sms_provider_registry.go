package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// NewSMSProvider 按 key 创建 SMSProvider 适配器实例。
// 所有供应商通过 init() 注册到 smsProviderFromRegistry，
// 新增供应商只需新增适配器文件并注册，无需改动此处。
func NewSMSProvider(key string, settings map[string]string, client *http.Client) (SMSProvider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if _, ok := SMSProviderDescriptorFor(key); !ok {
		return nil, errors.New("短信服务商不受支持")
	}
	provider, found, err := smsProviderFromRegistry(key, settings, client)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("短信服务商适配器未注册：" + key)
	}
	return provider, nil
}

// smsDoRequest 是所有短信适配器共用的 HTTP 请求执行函数。
// 负责传输层：请求构造、响应读取、大小限制、BOM 去除。
// 协议解析由各适配器自行处理。强制不跟随重定向以避免 SSRF 和重试失控。
func smsDoRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
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
		return nil, smsUnknownError{}
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, smsUnknownError{}
	}
	return bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), nil
}

func decodeSMSResponse(raw []byte) (map[string]any, error) {
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&out) != nil || out == nil || d.Decode(new(any)) != io.EOF {
		return nil, smsUnknownError{}
	}
	return out, nil
}

func smsRejected() (SMSProviderResult, error) {
	return SMSProviderResult{ProviderCode: "provider_rejected", Message: "短信供应商明确拒绝请求"}, errors.New("短信供应商明确拒绝请求，请检查供应商控制台")
}
