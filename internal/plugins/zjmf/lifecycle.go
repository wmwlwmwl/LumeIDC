package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
			ExpiryAt     any    `json:"nextduedate"`
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
	detail.ExpiryAt = anyToStr(hd.ExpiryAt)
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

func (p Provider) Renew(ctx context.Context, cfg server.Config, upstreamHostID int64, cycle string, ck server.CheckpointStore) error {
	cycle, ok := cycleMap[cycle]
	if !ok {
		return fmt.Errorf("不支持的计费周期: %s", cycle)
	}
	// 步骤1: 复用已建的续费账单；没有才去上游新建（/host/renew 只建账单不建订单）。
	var invoiceID string
	if ck != nil {
		v, ok, err := ck.GetCheckpoint(ckRenewInvoice)
		if err != nil {
			return fmt.Errorf("读取续费账单检查点失败: %w", err)
		}
		if ok {
			invoiceID = v
		}
	}
	if invoiceID == "" {
		form := url.Values{
			"hostid":        {strconv.FormatInt(upstreamHostID, 10)},
			"billingcycles": {cycle},
		}
		var ren struct {
			Data struct {
				InvoiceID flexString `json:"invoiceid"`
			} `json:"data"`
		}
		if err := postForm(ctx, cfg, "/host/renew", form, &ren); err != nil {
			return fmt.Errorf("续费下单失败: %w", err)
		}
		invoiceID = string(ren.Data.InvoiceID)
		if !validInvoiceID(invoiceID) {
			// 上游未生成账单 = 无需付款（0 元商品的续费上游直接续期生效）。
			// 必须按成功处理，不能当失败重试：每重试一次 /host/renew 都会让上游再多续一期。
			log.Printf("[zjmf] host %d 续费未返回账单（invoiceid=%q），按上游已直接续期处理", upstreamHostID, invoiceID)
			return nil
		}
		if ck != nil {
			if err := ck.SetCheckpoint(ckRenewInvoice, invoiceID); err != nil {
				// 账单已建但检查点没落库：重试会再建一张，交人工而不是硬重试。
				return &server.ManualReviewError{
					Msg:               fmt.Sprintf("保存续费账单检查点失败（已建账单 invoice=%s）: %v", invoiceID, err),
					UpstreamInvoiceID: invoiceID,
				}
			}
		}
	}
	// 步骤2: 用余额支付该续费账单（与开通 apply_credit 一致），使续费真正生效。
	if err := postForm(ctx, cfg, "/apply_credit", url.Values{
		"invoiceid":  {invoiceID},
		"use_credit": {"1"},
		"enough":     {"1"},
	}, &map[string]any{}); err != nil {
		// 账单已失效（被删除/作废）→ 重试永远付不掉：清掉检查点让重试时重新建单，并转人工。
		if unusable, perr := upstreamInvoiceUnusable(ctx, cfg, invoiceID); perr != nil {
			return &server.ManualReviewError{Msg: "上游续费账单已付或状态不明确，保留检查点，请核对", UpstreamInvoiceID: invoiceID}
		} else if unusable {
			if ck != nil {
				if derr := ck.DeleteCheckpoint(ckRenewInvoice); derr != nil {
					log.Printf("[zjmf] 清除失效续费账单检查点失败（invoice %s）: %v", invoiceID, derr)
				}
			}
			return &server.ManualReviewError{
				Msg: fmt.Sprintf("上游续费账单 %s 已失效（不存在或已作废），已清除其检查点，"+
					"请确认上游无残留订单后重试: %v", invoiceID, err),
				UpstreamInvoiceID: invoiceID,
			}
		}
		// 账单还在（多为上游余额不足）：保持重试、不消耗重试次数，充值到账后自动付掉。
		return &server.RetryLaterError{
			Msg: fmt.Sprintf("上游续费账单 %s 支付失败（请检查上游余额）: %v", invoiceID, err),
		}
	}
	// 续费成功：清掉账单检查点，否则下次续费（新订单）会误复用这张已付账单。
	if ck != nil {
		if err := ck.DeleteCheckpoint(ckRenewInvoice); err != nil {
			log.Printf("[zjmf] 清除续费账单检查点失败（host %d，invoice %s）: %v", upstreamHostID, invoiceID, err)
		}
	}
	return nil
}

// Suspend 停机：POST /provision/default {id, func: suspend}。
// 魔方财务 home 模块 ProvisionController::execute 支持 suspend/unsuspend（旧代码调
// /v1/hosts/:id/module/suspend，openapi 无该端点，必然 404）。
func (p Provider) Suspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return defaultModuleAction(ctx, cfg, upstreamHostID, "suspend", nil)
}

// Unsuspend 恢复：POST /provision/default {id, func: unsuspend}。
func (p Provider) Unsuspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return defaultModuleAction(ctx, cfg, upstreamHostID, "unsuspend", nil)
}

// Terminate ZJMF 无公开删除端点，用停机替代并标记本地 terminated。
// ponytail: 上游真实删除依赖其自动清理策略；如需硬删二期走 setdownstream 解绑。
func (p Provider) Terminate(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	return p.Suspend(ctx, cfg, upstreamHostID)
}

// hostGonePhrases 上游表达「该实例已不存在」的常见措辞。
// 实测（2026-09-17，ccyidc 上游）：对已释放/不存在的 host_id，魔方财务回
// HTTP 200 + {"status":406,"msg":"未找到该产品"}——不给 domainstatus，只给业务错误码 + 中文 msg，
// 与账单失效同一套路（见 upstreamInvoiceUnusable）。因此这里必须靠文案判定。
// 只匹配资源缺失类措辞，绝不匹配 token/权限/参数类——否则鉴权异常会被误判成实例已删，
// 进而批量误删本地服务（同步侧另有按服务器的批量护栏兜底）。
var hostGonePhrases = []string{"不存在", "未找到", "已删除", "已被删除", "无此", "not found", "no such host"}

// isHostGoneMsg 判断上游业务错误文案是否为「实例不存在」。
func isHostGoneMsg(msg string) bool {
	low := strings.ToLower(msg)
	for _, p := range hostGonePhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
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
		// 只有业务错误（上游明确答复）才可能是"实例已不存在"；
		// 网络/超时/HTTP/鉴权失败原样上抛，由同步侧区分为"我方故障"，不计缺失分。
		if isBizError(err) {
			if isHostGoneMsg(err.Error()) {
				// 上游已释放：返回 terminated 走既有映射，本地随即置 status=3，不再挂幽灵服务。
				return server.ServiceStatus{Status: "terminated"}, nil
			}
			// 业务错误但措辞未识别：标记为"实例不可见"，由同步侧累计次数兜底判定。
			return server.ServiceStatus{}, fmt.Errorf("%w: %s", server.ErrHostMissing, err.Error())
		}
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
