package zjmf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/server"
)

// powerActions 允许的电源操作名（对齐 ZJMF module func）。
var powerActions = map[string]bool{
	"on": true, "off": true, "reboot": true,
	"hard_off": true, "hard_reboot": true, "boot": true,
}

// PowerAction POST /provision/default {id, func: action}
func (p Provider) PowerAction(ctx context.Context, cfg server.Config, hostID int64, action string) error {
	if !powerActions[action] {
		return fmt.Errorf("不支持的电源操作: %s", action)
	}
	return defaultModuleAction(ctx, cfg, hostID, action, nil)
}

// ResetPassword func=crack_pass。密码为空或不合上游规则时自动生成合规随机密码，
// 返回最终应用的密码供前端展示，避免上游魔方云模块密码规则校验失败。
func (p Provider) ResetPassword(ctx context.Context, cfg server.Config, hostID int64, password string) (string, error) {
	if password == "" || !validHostPassword(password) {
		password = randomHostPassword()
	}
	err := defaultModuleAction(ctx, cfg, hostID, "crack_pass",
		url.Values{"password": {password}})
	return password, err
}

// ReinstallOptions 从 host header 提取 cloud_os / cloud_os_group。
func (p Provider) ReinstallOptions(ctx context.Context, cfg server.Config, hostID int64) ([]server.OSOption, error) {
	var out struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/host/header?host_id="+strconv.FormatInt(hostID, 10)+"&source=API", &out); err != nil {
		return nil, err
	}
	groupNames := map[string]string{}
	var opts []server.OSOption
	// cloud_os_group: [{id,name}]
	if groups, ok := out.Data["cloud_os_group"]; ok {
		for _, g := range asArrayRaw(groups) {
			id := strings.TrimSpace(asString(g["id"]))
			name := strings.TrimSpace(asString(g["name"]))
			if id != "" && name != "" {
				groupNames[id] = name
			}
		}
	}
	// cloud_os: [{id,name,group}]
	if oss, ok := out.Data["cloud_os"]; ok {
		for _, o := range asArrayRaw(oss) {
			id := strings.TrimSpace(asString(o["id"]))
			name := strings.TrimSpace(asString(o["name"]))
			if id == "" || name == "" {
				continue
			}
			group := strings.TrimSpace(asString(o["group_name"]))
			if group == "" {
				group = groupNames[strings.TrimSpace(asString(o["group"]))]
			}
			opts = append(opts, server.OSOption{ID: id, Name: name, Group: group})
		}
	}
	return opts, nil
}

// Reinstall func=reinstall {os}
func (p Provider) Reinstall(ctx context.Context, cfg server.Config, hostID int64, osID string) error {
	if osID == "" {
		return fmt.Errorf("未选择操作系统")
	}
	return defaultModuleAction(ctx, cfg, hostID, "reinstall", url.Values{"os": {osID}})
}

// Rescue 进入救援模式：POST /provision/default {func: rescue_system, system, temp_pass}。
// system: 1=Windows, 2=Linux（对齐上游/ZJMF-CBAP 的取值）；temp_pass 为救援系统临时密码，
// 为空或不合规时自动生成。返回实际使用的密码供前端展示。
func (p Provider) Rescue(ctx context.Context, cfg server.Config, hostID int64, system string) error {
	_, err := p.RescueWithPass(ctx, cfg, hostID, system, "")
	return err
}

// RescueWithPass 带临时密码进入救援模式，返回最终应用的密码。
func (p Provider) RescueWithPass(ctx context.Context, cfg server.Config, hostID int64, system, tempPass string) (string, error) {
	if system != "1" && system != "2" {
		system = "2" // 默认 Linux
	}
	if tempPass == "" || !validHostPassword(tempPass) {
		tempPass = randomHostPassword()
	}
	err := defaultModuleAction(ctx, cfg, hostID, "rescue_system",
		url.Values{"system": {system}, "temp_pass": {tempPass}})
	return tempPass, err
}

// RescueState 救援模式状态（remoteInfo 走 /provision/custom 通道）。
func (p Provider) RescueState(ctx context.Context, cfg server.Config, hostID int64) (bool, error) {
	var out struct {
		Data struct {
			Rescue int `json:"rescue"`
		} `json:"data"`
	}
	if err := postForm(ctx, cfg, "/provision/custom/"+strconv.FormatInt(hostID, 10),
		url.Values{"func": {"remoteInfo"}}, &out); err != nil {
		return false, err
	}
	return out.Data.Rescue == 1, nil
}

// ExitRescue 退出救援模式（走 /provision/custom 通道，避免上游非标准响应格式误判为失败）。
func (p Provider) ExitRescue(ctx context.Context, cfg server.Config, hostID int64) error {
	_, err := customModuleAction(ctx, cfg, hostID, "exitRescue", nil)
	return err
}

// vncCache 缓存 VNC 会话信息（wss 地址 + 密码）：上游 func=vnc 每次返回新 token，
// 而详情页一次加载会触发多次 VNCInfo（页面+资产代理+ws 拨号），若每次都调上游会
// 互相刷新 token 导致拨号失败（上游 token 疑似一次性）。短缓存复用同一会话。
// ponytail: 进程内存缓存，多实例部署时各自独立；TTL 3 分钟足够完成一次连接建立。
var vncCache = struct {
	sync.Mutex
	m map[string]vncEntry
}{m: map[string]vncEntry{}}

type vncEntry struct {
	info   server.VNCInfo
	expiry time.Time
}

func vncCacheKey(cfg server.Config, hostID int64) string {
	return strings.TrimRight(cfg.APIURL, "/") + "|" + strconv.FormatInt(hostID, 10)
}

// InvalidateVNC 清除该实例的 VNC 会话缓存（拨号 bad handshake 时由 handler 调用）。
func (p Provider) InvalidateVNC(ctx context.Context, cfg server.Config, hostID int64) {
	vncCache.Lock()
	delete(vncCache.m, vncCacheKey(cfg, hostID))
	vncCache.Unlock()
}

// VNCInfo 拉取 func=vnc 并解析出：VNC 密码、上游 wss 隧道地址与静态资源源站。
// 上游把 wss 地址与密码放在 /dcim/novnc 页面的查询参数里。
// wss 地址服务端拨号用，绝不写入返回给浏览器的页面，避免泄露上游主机信息。
func (p Provider) VNCInfo(ctx context.Context, cfg server.Config, hostID int64) (server.VNCInfo, error) {
	key := vncCacheKey(cfg, hostID)
	vncCache.Lock()
	if e, ok := vncCache.m[key]; ok && time.Now().Before(e.expiry) {
		vncCache.Unlock()
		return e.info, nil
	}
	vncCache.Unlock()

	info, err := p.fetchVNCInfo(ctx, cfg, hostID)
	if err != nil {
		return server.VNCInfo{}, err
	}
	vncCache.Lock()
	vncCache.m[key] = vncEntry{info: info, expiry: time.Now().Add(3 * time.Minute)}
	vncCache.Unlock()
	return info, nil
}

func (p Provider) fetchVNCInfo(ctx context.Context, cfg server.Config, hostID int64) (server.VNCInfo, error) {
	var out struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	form := url.Values{"id": {strconv.FormatInt(hostID, 10)}, "func": {"vnc"}}
	if err := postForm(ctx, cfg, "/provision/default", form, &out); err != nil {
		return server.VNCInfo{}, err
	}
	pageURL := ""
	for _, key := range []string{"url", "vnc_url", "link"} {
		if raw, ok := out.Data[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				pageURL = s
				break
			}
		}
	}
	if pageURL == "" {
		return server.VNCInfo{}, server.ErrNotSupported
	}
	pu, err := url.Parse(pageURL)
	if err != nil {
		return server.VNCInfo{}, fmt.Errorf("无效的 VNC 地址")
	}
	origin := pu.Scheme + "://" + pu.Host
	q := pu.Query()
	res := server.VNCInfo{AssetOrigin: origin, Password: q.Get("password")}
	if enc := q.Get("url"); enc != "" {
		if raw, derr := base64.StdEncoding.DecodeString(enc); derr == nil {
			res.WebSocketURL = string(raw)
		}
	}
	if res.WebSocketURL == "" || res.Password == "" {
		return server.VNCInfo{}, server.ErrNotSupported
	}
	return res, nil
}

// Usage 从 host header 提取流量（字段因上游模块而异，宽松提取）。
func (p Provider) Usage(ctx context.Context, cfg server.Config, hostID int64) (server.UsageInfo, error) {
	var out struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/host/header?host_id="+strconv.FormatInt(hostID, 10)+"&source=API", &out); err != nil {
		return server.UsageInfo{}, err
	}
	d := out.Data
	info := server.UsageInfo{
		TrafficUsed:  num(rawNum(d["bw_usage"])) + num(rawNum(d["traffic_used"])),
		TrafficLimit: num(rawNum(d["bw_limit"])) + num(rawNum(d["traffic_limit"])),
	}
	return info, nil
}

// PowerStatus 实时电源状态：POST /provision/default {id, func=status, is_api=true}。
// 四态映射对齐 ZJMF-CBAP：task/process/cold_migrate/hot_migrate→operating，on/waiting→on，off→off，其余→fault。
func (p Provider) PowerStatus(ctx context.Context, cfg server.Config, hostID int64) (server.PowerStatus, error) {
	var out struct {
		Data struct {
			Status string `json:"status"`
			Desc   string `json:"des"`
		} `json:"data"`
	}
	form := url.Values{
		"id":     {strconv.FormatInt(hostID, 10)},
		"func":   {"status"},
		"is_api": {"true"},
	}
	if err := postForm(ctx, cfg, "/provision/default", form, &out); err != nil {
		return server.PowerStatus{}, err
	}
	return normalizePower(out.Data.Status, out.Data.Desc), nil
}

// normalizePower 把上游电源状态归一化为四态。
func normalizePower(raw, desc string) server.PowerStatus {
	var s string
	switch raw {
	case "on", "waiting":
		s = "on"
	case "off":
		s = "off"
	case "task", "process", "cold_migrate", "hot_migrate":
		s = "operating"
	default:
		s = "fault"
	}
	return server.PowerStatus{Status: s, Desc: desc}
}

// TrafficUsage 每日流量：GET /host/trafficusage?id=。
func (p Provider) TrafficUsage(ctx context.Context, cfg server.Config, hostID int64) ([]server.TrafficDay, error) {
	var out struct {
		Data []server.TrafficDay `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/host/trafficusage?id="+strconv.FormatInt(hostID, 10), &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// defaultModuleAction 所有实例操作的统一入口：POST /provision/default {id, func, ...}。
func defaultModuleAction(ctx context.Context, cfg server.Config, hostID int64, fn string, extra url.Values) error {
	form := url.Values{"id": {strconv.FormatInt(hostID, 10)}, "func": {fn}}
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	return postForm(ctx, cfg, "/provision/default", form, &map[string]any{})
}

// customModuleAction 走 /provision/custom/{hostID} 通道（doRaw，不强制校验 JSON 业务码），
// 适用于上游返回非标准响应格式的操作（如 exitRescue）。
func customModuleAction(ctx context.Context, cfg server.Config, hostID int64, fn string, extra url.Values) (string, error) {
	form := url.Values{"func": {fn}}
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return "", err
	}
	u := strings.TrimRight(cfg.APIURL, "/") + "/provision/custom/" + strconv.FormatInt(hostID, 10)
	return doRaw(ctx, cfg, http.MethodPost, u, "application/x-www-form-urlencoded", form.Encode(), "Bearer "+token)
}

// Chart 拉取监控图表时序：GET /provision/chart/{hostID}?type=&select=&is_api=true。
func (p Provider) Chart(ctx context.Context, cfg server.Config, hostID int64, typ, sel string) (server.ChartSeries, error) {
	if sel == "" {
		sel = "24h"
	}
	q := url.Values{"type": {typ}, "select": {sel}, "is_api": {"true"}}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg,
		"/provision/chart/"+strconv.FormatInt(hostID, 10)+"?"+q.Encode(), &out); err != nil {
		return server.ChartSeries{}, err
	}
	return parseChart(out.Data, typ)
}

// parseChart 把上游监控响应规整为 ChartSeries。
// 实测 finance 型返回：{unit, chart_type, label:[系列名...], list:[[ {time,value}... ], ...]}
// list 为「数组的数组」，按 label 分系列；兼容扁平 list / 纯数组等形态。
func parseChart(raw json.RawMessage, typ string) (server.ChartSeries, error) {
	series := server.ChartSeries{Type: typ}
	if len(raw) == 0 {
		return series, nil
	}
	// 形态1：{unit,label,list:[[...],[...]]} —— 真实上游形态（多系列）
	var obj struct {
		Unit  string               `json:"unit"`
		Label []string             `json:"label"`
		List  [][]map[string]json.RawMessage `json:"list"`
	}
	if json.Unmarshal(raw, &obj) == nil && len(obj.List) > 0 {
		series.Unit = obj.Unit
		for i, linePts := range obj.List {
			line := server.ChartLine{Points: []server.ChartPoint{}}
			if i < len(obj.Label) {
				line.Label = obj.Label[i]
			}
			for _, it := range linePts {
				line.Points = append(line.Points, chartPoint(it))
			}
			series.Lines = append(series.Lines, line)
		}
		return series, nil
	}
	// 形态2：{list:[{time,value}]} 扁平数组（单系列）
	var obj2 struct {
		List []map[string]json.RawMessage `json:"list"`
		Unit string                       `json:"unit"`
	}
	if json.Unmarshal(raw, &obj2) == nil && len(obj2.List) > 0 {
		series.Unit = obj2.Unit
		line := server.ChartLine{Points: []server.ChartPoint{}}
		for _, it := range obj2.List {
			line.Points = append(line.Points, chartPoint(it))
		}
		series.Lines = []server.ChartLine{line}
		return series, nil
	}
	// 形态3：数组 [{time,value}]（单系列）
	var arr []map[string]json.RawMessage
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		line := server.ChartLine{Points: []server.ChartPoint{}}
		for _, it := range arr {
			line.Points = append(line.Points, chartPoint(it))
		}
		series.Lines = []server.ChartLine{line}
		return series, nil
	}
	// 形态4：纯数值数组
	var nums []float64
	if json.Unmarshal(raw, &nums) == nil && len(nums) > 0 {
		line := server.ChartLine{Points: []server.ChartPoint{}}
		for i, v := range nums {
			line.Points = append(line.Points, server.ChartPoint{Time: strconv.Itoa(i), Value: v})
		}
		series.Lines = []server.ChartLine{line}
	}
	return series, nil
}

// chartPoint 从监控点对象里挑出时间/数值字段。
func chartPoint(it map[string]json.RawMessage) server.ChartPoint {
	p := server.ChartPoint{}
	for _, tk := range []string{"time", "date", "x", "t"} {
		if raw, ok := it[tk]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				p.Time = s
				break
			}
		}
	}
	for _, vk := range []string{"value", "y", "used", "usage", "val"} {
		if raw, ok := it[vk]; ok {
			if v, ok := rawNum2(raw); ok {
				p.Value = v
				break
			}
		}
	}
	return p
}

// rawNum2 解析宽松数值（字符串或数字）。
func rawNum2(raw json.RawMessage) (float64, bool) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return v, true
		}
	}
	return 0, false
}
