// Package easypanel 实现 kangle + EasyPanel 上游适配（whm API，默认端口 3312）。
// 文档参考：kanglesoft 社区与公开二次开发接口说明。
package easypanel

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	cryptorand "crypto/rand"

	"lumeidc/internal/server"
)

const apiPath = "/api/index.php"

// errAPI EasyPanel API 返回非 200 时的错误，携带 result 码（403 签名错误 / 500 业务失败等）。
type errAPI struct {
	code int
	msg  string
}

func (e *errAPI) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("EasyPanel API 错误(%d): %s", e.code, e.msg)
	}
	return fmt.Sprintf("EasyPanel API 错误(%d)", e.code)
}

// apiCode 提取错误的 result 码；非 API 错误返回 0。
func apiCode(err error) int {
	if e, ok := err.(*errAPI); ok {
		return e.code
	}
	return 0
}

// client EasyPanel whm API 请求器：s = md5(a + skey + r)，固定 c=whm&json=1。
type client struct {
	base string // http://host:port
	skey string
	http *http.Client
}

func newClient(cfg server.Config) *client {
	return &client{
		base: strings.TrimRight(cfg.APIURL, "/"),
		skey: cfg.APIKey,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// md5hex 计算签名 s = md5(a + skey + r)。
func md5hex(action, skey, r string) string {
	sum := md5.Sum([]byte(action + skey + r))
	return hex.EncodeToString(sum[:])
}

// call 发起一次 API 调用。params 为业务参数（不含 c/a/r/s/json）。
// result=200 返回整个响应 map；否则返回 errAPI（403 权限错误 / 500 业务失败）。
func (c *client) call(ctx context.Context, action string, params map[string]string) (map[string]any, error) {
	if c.base == "" || c.skey == "" {
		return nil, fmt.Errorf("EasyPanel 服务器未配置 API 地址或安全码")
	}
	// r 取随机 64 位数（防重放由上游按 r+s 校验）
	var rb [8]byte
	if _, err := cryptorand.Read(rb[:]); err != nil {
		return nil, fmt.Errorf("生成随机数失败: %w", err)
	}
	r := strconv.FormatUint(uint64(rb[0])<<56|uint64(rb[1])<<48|uint64(rb[2])<<40|uint64(rb[3])<<32|
		uint64(rb[4])<<24|uint64(rb[5])<<16|uint64(rb[6])<<8|uint64(rb[7]), 10)
	q := url.Values{}
	q.Set("c", "whm")
	q.Set("a", action)
	q.Set("r", r)
	q.Set("s", md5hex(action, c.skey, r))
	q.Set("json", "1")
	for k, v := range params {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+apiPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("EasyPanel 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("EasyPanel 响应读取失败: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("EasyPanel 响应解析失败: %.200s", string(body))
	}
	code, _ := out["result"].(float64)
	if int(code) != 200 {
		msg, _ := out["msg"].(string)
		return nil, &errAPI{code: int(code), msg: msg}
	}
	return out, nil
}

// strField 安全取响应字符串字段（EP 各字段类型不稳定，字符串/数字均可能）。
func strField(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// numField 安全取响应数字字段。
func numField(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	default:
		return 0
	}
}
