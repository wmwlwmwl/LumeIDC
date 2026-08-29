package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// fetchHostHeaderRaw 拉取 /host/header 一次，返回 data 原始字段，供 HostDetail/ModuleSummary 复用，
// 避免详情页对同一接口重复请求。
func fetchHostHeaderRaw(ctx context.Context, cfg server.Config, upstreamHostID int64) (map[string]json.RawMessage, error) {
	var out struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/host/header?host_id="+strconv.FormatInt(upstreamHostID, 10)+"&source=API", &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// HostDetail 实现 server.HostDetailFetcher：实时拉取实例登录与系统信息。
func (p Provider) HostDetail(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.HostDetail, error) {
	data, err := fetchHostHeaderRaw(ctx, cfg, upstreamHostID)
	if err != nil {
		return server.HostDetail{}, err
	}
	return parseHostDetail(data)
}

// parseHostDetail 从 /host/header 的 data 字段解析登录/系统信息。
func parseHostDetail(data map[string]json.RawMessage) (server.HostDetail, error) {
	var out struct {
		HostData struct {
			Domain       string `json:"domain"`
			DomainStatus string `json:"domainstatus"`
			IP           string `json:"dedicatedip"`
			AssignedIPs  any    `json:"assignedips"`
			Username     string `json:"username"`
			Password     string `json:"password"`
			Port         any    `json:"port"`
			OS           string `json:"os"`
			BWLimit      any    `json:"bwlimit"`
			BWUsage      any    `json:"bwusage"`
		} `json:"host_data"`
		ConfigOptions []struct {
			NameK   string `json:"name_k"`
			SubName string `json:"sub_name"`
			Group   string `json:"os_group"`
		} `json:"config_options"`
	}
	if raw, ok := data["host_data"]; ok {
		_ = json.Unmarshal(raw, &out.HostData)
	}
	if raw, ok := data["config_options"]; ok {
		_ = json.Unmarshal(raw, &out.ConfigOptions)
	}
	hd := out.HostData
	detail := server.HostDetail{
		IP:        hd.IP,
		Username:  hd.Username,
		Password:  hd.Password,
		Status:    domainStatusText(hd.DomainStatus),
		OSVersion: hd.OS,
	}
	if p, ok := hd.Port.(string); ok {
		detail.Port = p
	} else if n, ok := hd.Port.(float64); ok && n > 0 {
		detail.Port = strconv.FormatInt(int64(n), 10)
	}
	detail.AdditionalIPs = anyToStrSlice(hd.AssignedIPs)
	detail.BWLimit = anyToStr(hd.BWLimit)
	detail.BWUsage = anyToStr(hd.BWUsage)
	// 系统名称取自操作系统配置子项的 os_group（如 Ubuntu）；数据中心从配置项里识别。
	for _, o := range out.ConfigOptions {
		if o.NameK == "os" {
			detail.OSName = o.Group
			if detail.OSName == "" {
				detail.OSName = o.SubName
			}
			continue
		}
		if detail.Datacenter == "" && isDCField(o.NameK, o.SubName) {
			detail.Datacenter = o.SubName
		}
	}
	if detail.OSName == "" {
		detail.OSName = strings.SplitN(hd.OS, "-", 2)[0]
	}
	return detail, nil
}

// anyToStr 把上游松散类型（数字/字符串/空）统一成展示字符串。
func anyToStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	}
	return ""
}

// anyToStrSlice 把附加 IP 字段（可能为空 / 字符串逗号分隔 / 字符串数组）规整为切片。
func anyToStrSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		var out []string
		for _, e := range t {
			if s := anyToStr(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		return strings.Split(t, ",")
	}
	return nil
}

// isDCField 判断配置项是否为数据中心/机房/线路类（用于从 config_options 提取机房名）。
func isDCField(nameK, subName string) bool {
	for _, kw := range []string{"data", "line", "dc", "datacenter", "region", "area", "数据", "机房", "线路", "中心", "区域"} {
		if strings.Contains(strings.ToLower(nameK), kw) || strings.Contains(subName, kw) {
			return true
		}
	}
	return false
}

// domainStatusText 把上游 domainstatus 映射为中文（详情页实时展示）；未知状态原样返回。
func domainStatusText(s string) string {
	switch strings.ToLower(s) {
	case "active":
		return "运行中"
	case "pending":
		return "待开通"
	case "suspended":
		return "已停机"
	case "terminated":
		return "已删除"
	}
	return s
}

func (p Provider) Renew(ctx context.Context, cfg server.Config, upstreamHostID int64, cycle string) error {
	cycle, ok := cycleMap[cycle]
	if !ok {
		return fmt.Errorf("不支持的计费周期: %s", cycle)
	}
	form := url.Values{
		"hostid":        {strconv.FormatInt(upstreamHostID, 10)},
		"billingcycles": {cycle},
	}
	// 步骤1: 上游创建续费账单（/host/renew 只建账单不建订单），解析返回的账单号。
	var ren struct {
		Data struct {
			InvoiceID flexString `json:"invoiceid"`
		} `json:"data"`
	}
	if err := postForm(ctx, cfg, "/host/renew", form, &ren); err != nil {
		return fmt.Errorf("上游续费下单失败: %w", err)
	}
	invoiceID := string(ren.Data.InvoiceID)
	if invoiceID == "" {
		return fmt.Errorf("上游续费未返回账单号，无法支付")
	}
	// 步骤2: 用余额支付该续费账单（与开通 apply_credit 一致），使续费真正生效。
	payForm := url.Values{
		"invoiceid":  {invoiceID},
		"use_credit": {"1"},
		"enough":     {"1"},
	}
	if err := postForm(ctx, cfg, "/apply_credit", payForm, &map[string]any{}); err != nil {
		return fmt.Errorf("上游续费账单支付失败（请检查上游余额）: %w", err)
	}
	return nil
}

func (p Provider) Suspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return hostAction(ctx, cfg, "suspend", upstreamHostID)
}

func (p Provider) Unsuspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return hostAction(ctx, cfg, "unsuspend", upstreamHostID)
}

// Terminate ZJMF 无独立删除端点，一期用停机替代并标记本地 terminated。
// ponytail: 上游真实删除依赖其自动清理策略；如需硬删二期走 setdownstream 解绑。
func (p Provider) Terminate(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return p.Suspend(ctx, cfg, upstreamHostID)
}

func hostAction(ctx context.Context, cfg server.Config, action string, hostID int64) error {
	path := "/v1/hosts/" + strconv.FormatInt(hostID, 10) + "/module/" + action
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return err
	}
	return hostActionRequest(ctx, cfg, path, token, true)
}

func hostActionRequest(ctx context.Context, cfg server.Config, path, token string, allowRetry bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(cfg.APIURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := defaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("主机操作请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && allowRetry {
		cache.invalidate(authCacheKey(cfg))
		newToken, err := ensureToken(ctx, cfg)
		if err != nil {
			return err
		}
		return hostActionRequest(ctx, cfg, path, newToken, false)
	}
	body, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return fmt.Errorf("主机操作响应读取失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("主机操作失败: http %d: %.100s", resp.StatusCode, body)
	}
	var meta map[string]any
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("主机操作响应非 JSON: %.100s", body)
	}
	return checkBizOK(meta)
}

func (p Provider) Status(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.ServiceStatus, error) {
	var out struct {
		Data struct {
			// 真实响应中 domainstatus 位于 data.host_data 下（曾有解析到 data.domainstatus 的旧实现恒为 unknown）
			HostData struct {
				DomainStatus string `json:"domainstatus"`
				Domain       string `json:"domain"`
				Hostname     string `json:"hostname"`
			} `json:"host_data"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/host/header?host_id="+strconv.FormatInt(upstreamHostID, 10)+"&source=API", &out); err != nil {
		return server.ServiceStatus{}, err
	}
	st := out.Data.HostData.DomainStatus
	if st == "" {
		st = "unknown"
	}
	hn := out.Data.HostData.Domain
	if hn == "" {
		hn = out.Data.HostData.Hostname
	}
	return server.ServiceStatus{Status: st, Hostname: hn}, nil
}
