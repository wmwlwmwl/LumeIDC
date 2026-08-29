package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// blockFuncs 允许下发的方块操作白名单（对齐上游 module func 名）。
var blockFuncs = map[string]bool{
	"addNatAcl": true, "delNatAcl": true,
	"addNatWeb": true, "delNatWeb": true,
	"createSecurityGroup": true, "delSecurityGroup": true, "linkSecurityGroup": true,
	"createSecurityRule": true, "delSecurityRule": true,
	"mountIso": true, "setBootOrder": true,
}

// NatList 实现 server.ModuleBlocksProvider：解析 nat_acl 方块 HTML 表格。
func (p Provider) NatList(ctx context.Context, cfg server.Config, upstreamHostID int64) ([]server.NatRule, error) {
	content, err := p.ModulePage(ctx, cfg, upstreamHostID, "nat_acl")
	if err != nil {
		return nil, err
	}
	var out []server.NatRule
	for _, r := range parseTableRows(content) {
		if len(r.Cells) < 4 || r.Header {
			continue
		}
		id := int64(0)
		if r.ID != "" {
			id, _ = strconv.ParseInt(r.ID, 10, 64)
		}
		out = append(out, server.NatRule{
			ID:       id,
			Name:     r.Cells[0],
			External: r.Cells[1],
			Internal: r.Cells[2],
			Protocol: r.Cells[3],
		})
	}
	return out, nil
}

// NatWebList 实现 server.ModuleBlocksProvider：解析 nat_web 方块 HTML 表格。
func (p Provider) NatWebList(ctx context.Context, cfg server.Config, upstreamHostID int64) ([]server.NatWebEntry, error) {
	content, err := p.ModulePage(ctx, cfg, upstreamHostID, "nat_web")
	if err != nil {
		return nil, err
	}
	var out []server.NatWebEntry
	for _, r := range parseTableRows(content) {
		if len(r.Cells) < 3 || r.Header {
			continue
		}
		id, _ := strconv.ParseInt(r.ID, 10, 64)
		out = append(out, server.NatWebEntry{
			ID:       id,
			Domain:   r.Cells[0],
			External: r.Cells[1],
			Internal: r.Cells[2],
		})
	}
	return out, nil
}

// SecurityGroups 实现 server.ModuleBlocksProvider：解析 security_groups 方块 HTML 表格。
func (p Provider) SecurityGroups(ctx context.Context, cfg server.Config, upstreamHostID int64) ([]server.SecurityGroup, error) {
	content, err := p.ModulePage(ctx, cfg, upstreamHostID, "security_groups")
	if err != nil {
		return nil, err
	}
	var out []server.SecurityGroup
	for _, r := range parseTableRows(content) {
		if len(r.Cells) < 2 || r.Header {
			continue
		}
		id, _ := strconv.ParseInt(r.ID, 10, 64)
		out = append(out, server.SecurityGroup{
			ID:          id,
			Name:        r.Cells[0],
			Description: r.Cells[1],
		})
	}
	return out, nil
}

// SecurityRules 实现 server.ModuleBlocksProvider：showSecurityRules 返回 JSON list。
func (p Provider) SecurityRules(ctx context.Context, cfg server.Config, upstreamHostID int64, groupID int64) ([]server.SecurityRule, error) {
	raw, err := p.ModuleAction(ctx, cfg, upstreamHostID, url.Values{
		"func": {"showSecurityRules"},
		"id":   {strconv.FormatInt(groupID, 10)},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			List []struct {
				ID          any    `json:"id"`
				Description string `json:"description"`
				Action      string `json:"action"`
				Direction   string `json:"direction"`
				Protocol    string `json:"protocol"`
				StartPort   any    `json:"start_port"`
				EndPort     any    `json:"end_port"`
				IP          string `json:"ip"`
				StartIP     string `json:"start_ip"`
				EndIP       string `json:"end_ip"`
				Priority    any    `json:"priority"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("规则数据解析失败: %w", err)
	}
	var rules []server.SecurityRule
	for _, it := range out.Data.List {
		r := server.SecurityRule{
			ID:          anyToInt64(it.ID),
			Description: it.Description,
			Action:      it.Action,
			Direction:   it.Direction,
			Protocol:    it.Protocol,
			IP:          it.IP,
		}
		sp, ep := anyToStr(it.StartPort), anyToStr(it.EndPort)
		if sp == ep {
			r.PortRange = sp
		} else {
			r.PortRange = sp + "-" + ep
		}
		if r.IP == "" && it.StartIP != "" {
			r.IP = it.StartIP
			if it.EndIP != "" && it.EndIP != it.StartIP {
				r.IP += "-" + it.EndIP
			}
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// SettingData 实现 server.ModuleBlocksProvider：解析 setting 方块的下拉（ISO/启动顺序）。
func (p Provider) SettingData(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.SettingData, error) {
	content, err := p.ModulePage(ctx, cfg, upstreamHostID, "setting")
	if err != nil {
		return server.SettingData{}, err
	}
	return server.SettingData{
		Iso:  parseSelectOptions(content, "iso"),
		Boot: parseSelectOptions(content, "drive"),
	}, nil
}

// BlockAction 实现 server.ModuleBlocksProvider：白名单 + ModuleAction 通道。
func (p Provider) BlockAction(ctx context.Context, cfg server.Config, upstreamHostID int64, fn string, form url.Values) (string, error) {
	if !blockFuncs[fn] {
		return "", fmt.Errorf("不支持的方块操作: %s", fn)
	}
	f := url.Values{}
	for k, vs := range form {
		for _, v := range vs {
			f.Add(k, v)
		}
	}
	f.Set("func", fn)
	return p.ModuleAction(ctx, cfg, upstreamHostID, f)
}

// ---------- HTML 解析（表格结构固定，正则足够） ----------

// tableRow 表格行：单元格文本 + 行内首个 data-id + 是否表头。
type tableRow struct {
	Cells  []string
	ID     string
	Header bool
}

var (
	trRE      = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	cellRE    = regexp.MustCompile(`(?s)<t[dh][^>]*>(.*?)</t[dh]>`)
	idRE      = regexp.MustCompile(`data-id="(\d+)"`)
	tagRE     = regexp.MustCompile(`<[^>]+>`)
	selRE     = regexp.MustCompile(`(?s)<select[^>]*id="([a-z]+)"[^>]*>(.*?)</select>`)
	optRE     = regexp.MustCompile(`(?s)<option[^>]*value="([^"]*)"[^>]*>(.*?)</option>`)
	scriptRE  = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
)

// parseTableRows 解析 HTML 表格所有行（先剥掉 script，避免 JS 模板串被当成行）。
func parseTableRows(content string) []tableRow {
	content = scriptRE.ReplaceAllString(content, "")
	var rows []tableRow
	for _, m := range trRE.FindAllStringSubmatch(content, -1) {
		row := tableRow{Header: strings.Contains(m[1], "<th")}
		if id := idRE.FindStringSubmatch(m[1]); id != nil {
			row.ID = id[1]
		}
		for _, c := range cellRE.FindAllStringSubmatch(m[1], -1) {
			txt := strings.TrimSpace(tagRE.ReplaceAllString(c[1], ""))
			txt = strings.Join(strings.Fields(txt), " ")
			row.Cells = append(row.Cells, txt)
		}
		if len(row.Cells) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

// parseSelectOptions 解析指定 id 的 select 下拉选项（含 selected）。
func parseSelectOptions(content, selectID string) []server.SelectOption {
	for _, m := range selRE.FindAllStringSubmatch(content, -1) {
		if !strings.Contains(m[1], selectID) {
			continue
		}
		var opts []server.SelectOption
		for _, o := range optRE.FindAllStringSubmatch(m[2], -1) {
			opts = append(opts, server.SelectOption{
				Value:    o[1],
				Name:     strings.TrimSpace(tagRE.ReplaceAllString(o[2], "")),
				Selected: strings.Contains(o[0], "selected"),
			})
		}
		return opts
	}
	return nil
}
