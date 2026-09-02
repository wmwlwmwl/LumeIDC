package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// ModuleSummary 实现 server.ModuleProvider：从 /host/header 读取魔方云模块清单。
// 上游把各模块（快照/安全组/NAT 转发/共享建站等）统一暴露为
// module_client_area / module_button / module_chart，前台据此动态渲染，无需为每个模块写死。
func (p Provider) ModuleSummary(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.ModuleSummary, error) {
	data, err := fetchHostHeaderRaw(ctx, cfg, upstreamHostID)
	if err != nil {
		return server.ModuleSummary{}, err
	}
	return parseModules(data), nil
}

// parseModules 从 /host/header 的 data 字段解析模块清单（方块/按钮/图表开关）。
func parseModules(data map[string]json.RawMessage) server.ModuleSummary {
	sum := server.ModuleSummary{}
	// module_client_area: [{key,name},...]
	if raw, ok := data["module_client_area"]; ok {
		for _, it := range asArrayRaw(raw) {
			key := strings.TrimSpace(asString(it["key"]))
			if key == "" {
				continue
			}
			sum.Areas = append(sum.Areas, server.ModuleArea{
				Key:  key,
				Name: strings.TrimSpace(asString(it["name"])),
			})
		}
	}
	// module_button: 数组 或 分组 map(组名=>数组)
	if raw, ok := data["module_button"]; ok {
		sum.Buttons = appendModuleButtons(sum.Buttons, raw)
	}
	// module_chart: true/1 或 数组（启用的图表类型）
	if raw, ok := data["module_chart"]; ok {
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			sum.HasChart = b
		} else {
			var arr []any
			if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
				sum.HasChart = true
			}
		}
	}
	return sum
}

// HostOverview 实现 server.HostOverviewFetcher：一次 /host/header 同时解析登录/系统信息与模块清单，
// 详情页无需对同一接口请求两次。
func (p Provider) HostOverview(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.HostOverview, error) {
	data, err := fetchHostHeaderRaw(ctx, cfg, upstreamHostID)
	if err != nil {
		return server.HostOverview{}, err
	}
	detail, _ := parseHostDetail(data)
	return server.HostOverview{Detail: detail, Summary: parseModules(data)}, nil
}

func appendModuleButtons(dst []server.ModuleButton, raw json.RawMessage) []server.ModuleButton {
	// 先按列表解析
	var list []map[string]any
	if json.Unmarshal(raw, &list) == nil {
		for _, it := range list {
			dst = appendModuleButton(dst, it, "")
		}
		return dst
	}
	// 再按分组 map(组名=>数组) 解析
	var groups map[string]json.RawMessage
	if json.Unmarshal(raw, &groups) == nil {
		for group, g := range groups {
			for _, it := range asArrayRaw(g) {
				dst = appendModuleButton(dst, it, group)
			}
		}
	}
	return dst
}

func appendModuleButton(dst []server.ModuleButton, it map[string]any, group string) []server.ModuleButton {
	fn := strings.TrimSpace(asString(it["function"]))
	if fn == "" {
		fn = strings.TrimSpace(asString(it["func"]))
	}
	if fn == "" {
		return dst
	}
	return append(dst, server.ModuleButton{
		Type:     strings.TrimSpace(asString(it["type"])),
		Function: fn,
		Name:     strings.TrimSpace(asString(it["name"])),
		Group:    group,
	})
}

// ModulePage 实现 server.ModuleProvider：拉取某方块页面 HTML。
// 上游 /provision/custom/content 返回模块方块页面（可能为 JSON 包裹），经本地代理内嵌，隐藏上游域名。
func (p Provider) ModulePage(ctx context.Context, cfg server.Config, upstreamHostID int64, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("模块标识为空")
	}
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return "", err
	}
	u := strings.TrimRight(cfg.APIURL, "/") + "/provision/custom/content?" + url.Values{
		"id":  {strconv.FormatInt(upstreamHostID, 10)},
		"key": {key},
		"jwt": {token},
	}.Encode()
	body, err := doRaw(ctx, cfg, http.MethodGet, u, "", "", "Bearer "+token)
	if err != nil {
		return "", err
	}
	return normalizeModulePageBody(body), nil
}

// ModuleAction 实现 server.ModuleProvider：提交方块表单到上游 /provision/custom/{id}，
// 返回上游响应原文（可能是 JSON {status,msg} 或页面/HTML）。
func (p Provider) ModuleAction(ctx context.Context, cfg server.Config, upstreamHostID int64, form url.Values) (string, error) {
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return "", err
	}
	f := url.Values{}
	for k, vs := range form {
		if k == "_csrf" {
			continue // 本地 CSRF，不转发到上游
		}
		for _, v := range vs {
			f.Add(k, v)
		}
	}
	// id：上游 provision/custom/{hostID} 路径已定位主机；表单 id 是操作对象（如转发条目 id），
	// 仅当表单未带 id 时才补主机 id（部分旧模块按 id 定位主机）。
	if f.Get("id") == "" {
		f.Set("id", strconv.FormatInt(upstreamHostID, 10))
	}
	u := strings.TrimRight(cfg.APIURL, "/") + "/provision/custom/" + strconv.FormatInt(upstreamHostID, 10)
	return doRaw(ctx, cfg, http.MethodPost, u, "application/x-www-form-urlencoded", f.Encode(), "Bearer "+token)
}

// doRaw 发起带鉴权头的请求并读取原始正文；401 自动清缓存重登重试一次。
// 模块接口返回 JSON 或普通文本，故不做 JSON 业务码强校验（仅当正文是标准包装 JSON 时校验）。
func doRaw(ctx context.Context, cfg server.Config, method, u, contentType, reqBody, auth string) (string, error) {
	return doRawRetry(ctx, cfg, method, u, contentType, reqBody, auth, true)
}

func doRawRetry(ctx context.Context, cfg server.Config, method, u, contentType, reqBody, auth string, allowRetry bool) (string, error) {
	var reader io.Reader
	if reqBody != "" {
		reader = strings.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", auth)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := defaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("模块请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && allowRetry {
		cache.invalidate(authCacheKey(cfg))
		newToken, terr := ensureToken(ctx, cfg)
		if terr != nil {
			return "", terr
		}
		return doRawRetry(ctx, cfg, method, u, contentType, reqBody, authPrefixOf(auth)+newToken, false)
	}
	body, err := readLimited(resp.Body, 2<<20)
	if err != nil {
		return "", fmt.Errorf("模块响应读取失败: %w", err)
	}
	// 标准包装 JSON 时校验业务码；方块页为纯 HTML 时跳过。
	if err := checkBizAbnormal(body); err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 120 {
			msg = msg[:120]
		}
		return "", fmt.Errorf("模块请求失败 (http %d): %s", resp.StatusCode, msg)
	}
	return string(body), nil
}

// checkBizAbnormal 标准包装 JSON 校验业务码；非 JSON 返回 nil（跳过）。
func checkBizAbnormal(body []byte) error {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	if s, ok := m["status"].(float64); ok {
		if (s >= 200 && s < 300) || s == 1000 || s == 1001 {
			return nil
		}
		if c, ok := m["code"].(float64); ok && c == 0 {
			return nil
		}
		// 业务失败：取 msg 报错
		return apiErr{Msg: bizErrMsg(m)}
	}
	return nil
}

func bizErrMsg(m map[string]any) string {
	for _, k := range []string{"msg", "message"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return "上游返回错误"
}

// authPrefixOf 保留鉴权头前缀（Bearer / JWT），仅替换 token 以便重试。
func authPrefixOf(auth string) string {
	if i := strings.LastIndex(auth, " "); i >= 0 {
		return auth[:i+1]
	}
	return auth + " "
}

// normalizeModulePageBody 剥离 BOM；若为 JSON 包裹则优先取 data.html/content/view/template 字段。
func normalizeModulePageBody(body string) string {
	body = strings.TrimPrefix(body, "\uFEFF")
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return body
	}
	payload, ok := m["data"]
	if !ok {
		payload = m
	}
	if s, ok := payload.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if pm, ok := payload.(map[string]any); ok {
		for _, key := range []string{"html", "content", "view", "template"} {
			if s, ok := pm[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return body
}
