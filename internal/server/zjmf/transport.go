package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lumeidc/internal/server"
)

var defaultClient = &http.Client{Timeout: 30 * time.Second}

type apiErr struct{ Msg string }

func (e apiErr) Error() string { return e.Msg }

// flexString 兼容上游字段可能是字符串或数字（如 invoiceid 常返回数值 1002939）。
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	t := strings.Trim(string(b), `"`)
	*s = flexString(t)
	return nil
}

// getJSON 带 Bearer 的 GET；401 自动清缓存重登一次。
func getJSON(ctx context.Context, cfg server.Config, path string, out any) error {
	return doJSON(ctx, cfg, http.MethodGet, path, nil, out, true)
}

// postForm 带 Bearer 的 POST form。
func postForm(ctx context.Context, cfg server.Config, path string, form url.Values, out any) error {
	return doJSON(ctx, cfg, http.MethodPost, path, form, out, true)
}

func doJSON(ctx context.Context, cfg server.Config, method, path string, form url.Values, out any, allowRetry bool) error {
	raw, err := doJSONRaw(ctx, cfg, method, path, form, allowRetry)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s 响应解析失败: %w", path, err)
		}
	}
	return nil
}

func doJSONRaw(ctx context.Context, cfg server.Config, method, path string, form url.Values, allowRetry bool) ([]byte, error) {
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.APIURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := defaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && allowRetry {
		cache.invalidate(authCacheKey(cfg))
		return doJSONRaw(ctx, cfg, method, path, form, false)
	}
	raw, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("%s 响应读取失败: %w", path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s 请求失败: http %d: %.100s", path, resp.StatusCode, raw)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("%s 响应非 JSON: %.100s", path, raw)
	}
	if err := checkBizOK(meta); err != nil {
		return nil, err
	}
	return raw, nil
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("响应超过 %d 字节限制", max)
	}
	return b, nil
}

// checkBizOK 校验上游业务码：status==200/1000/1001 或 code==0 视为成功。
func checkBizOK(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if s, ok := m["status"].(float64); ok {
		if s >= 200 && s < 300 {
			return nil
		}
		if s == 1000 || s == 1001 {
			return nil
		}
	}
	if c, ok := m["code"].(float64); ok && c == 0 {
		return nil
	}
	msg := "上游返回错误"
	for _, k := range []string{"msg", "message"} {
		if s, ok := m[k].(string); ok && s != "" {
			msg = s
			break
		}
	}
	return apiErr{Msg: msg}
}

// settleResp 结算接口响应：data.invoiceid（可能为数值或字符串），data.hostids 部分版本返回。
type settleResp struct {
	Data struct {
		InvoiceID flexString `json:"invoiceid"`
		HostIDs   []int64    `json:"hostids"`
	} `json:"data"`
}

// postFormSettle 结算接口专用：返回原始 body + 解析后的账单/host 信息。
func postFormSettle(ctx context.Context, cfg server.Config, path string, form url.Values) (string, settleResp, error) {
	return postFormSettleRetry(ctx, cfg, path, form, true)
}

func postFormSettleRetry(ctx context.Context, cfg server.Config, path string, form url.Values, allowRetry bool) (string, settleResp, error) {
	var out settleResp
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return "", out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.APIURL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", out, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := defaultClient.Do(req)
	if err != nil {
		return "", out, fmt.Errorf("%s 请求失败: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && allowRetry {
		cache.invalidate(authCacheKey(cfg))
		return postFormSettleRetry(ctx, cfg, path, form, false)
	}
	body, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return "", out, fmt.Errorf("%s 响应读取失败: %w", path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return string(body), out, fmt.Errorf("%s 请求失败: http %d: %.100s", path, resp.StatusCode, body)
	}
	var meta map[string]any
	if err := json.Unmarshal(body, &meta); err != nil {
		return string(body), out, fmt.Errorf("%s 响应非 JSON: %.100s", path, body)
	}
	if err := checkBizOK(meta); err != nil {
		return string(body), out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return string(body), out, fmt.Errorf("%s 响应解析失败: %w", path, err)
	}
	return string(body), out, nil
}
