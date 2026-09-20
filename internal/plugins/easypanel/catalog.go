package easypanel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"lumeidc/internal/server"
)

// Catalog 拉取 EasyPanel 后台“网站管理 -> 产品列表”的本地产品。
//
// EasyPanel 没有标准化的 list_products WHM action，但 migrate_list_product
// 会返回 Base64(JSON(products))。产品没有标准价格字段，因此价格保持为 0；
// 产品 ID、名称和资源字段仍可用于目录展示、PID 绑定和后续开通。
func (p Provider) Catalog(ctx context.Context, cfg server.Config) ([]server.UpstreamProduct, error) {
	c := newClient(cfg)
	out, err := c.call(ctx, "migrate_list_product", nil)
	if err != nil {
		// 面板没有产品时返回 204，按空目录处理而不是把导入页面报成失败。
		if apiCode(err) == 204 {
			return []server.UpstreamProduct{}, nil
		}
		return nil, fmt.Errorf("EasyPanel 拉取产品列表失败: %w", err)
	}

	encoded := strings.TrimSpace(strField(out, "products"))
	if encoded == "" {
		return []server.UpstreamProduct{}, nil
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("EasyPanel 产品列表 Base64 解码失败: %w", err)
	}

	var raw []map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("EasyPanel 产品列表 JSON 解析失败: %w", err)
	}

	products := make([]server.UpstreamProduct, 0, len(raw))
	for _, item := range raw {
		pid := int(numField(item, "id"))
		if pid <= 0 {
			continue
		}
		name := strings.TrimSpace(strField(item, "product_name"))
		if name == "" {
			name = strings.TrimSpace(strField(item, "name"))
		}
		if name == "" {
			name = fmt.Sprintf("EasyPanel 产品 %d", pid)
		}

		products = append(products, server.UpstreamProduct{
			PID:         pid,
			Name:        name,
			GroupName:   "",
			Stock:       -1,
			Description: easyPanelProductDescription(item),
		})
	}

	if len(raw) > 0 && len(products) == 0 {
		return nil, fmt.Errorf("EasyPanel 产品列表没有可识别的产品 ID")
	}
	return products, nil
}

// CatalogLight 目录页只需要 PID/名称/分组；EasyPanel 的产品列表本身已经是轻量数据。
func (p Provider) CatalogLight(ctx context.Context, cfg server.Config) ([]server.UpstreamProduct, error) {
	return p.Catalog(ctx, cfg)
}

func easyPanelProductDescription(item map[string]any) string {
	parts := make([]string, 0, 3)
	if v := easyPanelQuotaText(strField(item, "web_quota")); v != "" {
		parts = append(parts, "网页空间 "+v)
	}
	if v := easyPanelQuotaText(strField(item, "db_quota")); v != "" {
		parts = append(parts, "数据库 "+v)
	}
	if v := strings.TrimSpace(strField(item, "domain")); v != "" {
		if v == "-1" {
			parts = append(parts, "域名不限")
		} else if v != "0" {
			parts = append(parts, "域名 "+v)
		}
	}
	return strings.Join(parts, "<br>")
}

func easyPanelQuotaText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return ""
	}
	if strings.HasSuffix(strings.ToUpper(value), "M") || strings.HasSuffix(strings.ToUpper(value), "G") {
		return value
	}
	return value + "M"
}
