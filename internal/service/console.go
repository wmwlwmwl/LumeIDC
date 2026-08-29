package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// Console 用户侧实例控制台操作（含归属校验）。
type Console struct {
	DB        *sql.DB
	Servers   *repo.Servers
	Products  *repo.Products
	Providers *server.Registry
}

// resolve 校验归属并返回 provider+cfg+hostID。无上游绑定返回 errNoUpstream。
func (c *Console) resolve(ctx context.Context, userID, serviceID int64) (server.ConsoleProvider, server.Config, int64, error) {
	var serverID sql.NullInt64
	var hostID int64
	var upstreamPID int64
	var providerCode string
	err := c.DB.QueryRowContext(ctx,
		`SELECT sv.upstream_host_id, coalesce(sv.server_id,p.server_id),
		        coalesce(nullif(sv.upstream_pid,0),p.upstream_pid),
		        coalesce(nullif(sv.upstream_provider,''),srv.provider,'')
		 FROM services sv JOIN products p ON p.id=sv.product_id
		 LEFT JOIN servers srv ON srv.id=coalesce(sv.server_id,p.server_id)
		 WHERE sv.id=$1 AND sv.user_id=$2 AND sv.status IN (1,2)`,
		serviceID, userID).Scan(&hostID, &serverID, &upstreamPID, &providerCode)
	if err != nil {
		return nil, server.Config{}, 0, fmt.Errorf("服务不存在或不可操作")
	}
	if !serverID.Valid || upstreamPID == 0 || hostID == 0 {
		return nil, server.Config{}, 0, errNoUpstream
	}
	prov, err := c.Providers.Get(providerCode)
	if err != nil {
		return nil, server.Config{}, 0, err
	}
	sv, err := c.Servers.Get(ctx, serverID.Int64)
	if err != nil {
		return nil, server.Config{}, 0, fmt.Errorf("读取服务器失败: %w", err)
	}
	cp, ok := prov.(server.ConsoleProvider)
	if !ok {
		return nil, server.Config{}, 0, server.ErrNotSupported
	}
	return cp, upstreamConfig(sv), hostID, nil
}

// Power 电源操作。
func (c *Console) Power(ctx context.Context, userID, serviceID int64, action string) error {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return err
	}
	return prov.PowerAction(ctx, cfg, hostID, action)
}

// ResetPassword 重置实例密码；返回最终应用的密码（为空/不合规时上游生成）。
func (c *Console) ResetPassword(ctx context.Context, userID, serviceID int64, password string) (string, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	return prov.ResetPassword(ctx, cfg, hostID, password)
}

// Rescue 进入救援模式（system: "1"=Windows, "2"=Linux）。
func (c *Console) Rescue(ctx context.Context, userID, serviceID int64, system string) error {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return err
	}
	return prov.Rescue(ctx, cfg, hostID, system)
}

// RescueWithPass 带临时密码进入救援模式，返回最终应用的密码。
func (c *Console) RescueWithPass(ctx context.Context, userID, serviceID int64, system, tempPass string) (string, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	rp, ok := prov.(server.RescueProvider)
	if !ok {
		return "", prov.Rescue(ctx, cfg, hostID, system)
	}
	return rp.RescueWithPass(ctx, cfg, hostID, system, tempPass)
}

// RescueState 救援模式是否开启。
func (c *Console) RescueState(ctx context.Context, userID, serviceID int64) (bool, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return false, err
	}
	rp, ok := prov.(server.RescueProvider)
	if !ok {
		return false, server.ErrNotSupported
	}
	return rp.RescueState(ctx, cfg, hostID)
}

// ExitRescue 退出救援模式。
func (c *Console) ExitRescue(ctx context.Context, userID, serviceID int64) error {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return err
	}
	rp, ok := prov.(server.RescueProvider)
	if !ok {
		return server.ErrNotSupported
	}
	return rp.ExitRescue(ctx, cfg, hostID)
}

// Reinstall 重装系统。
func (c *Console) Reinstall(ctx context.Context, userID, serviceID int64, osID string) error {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return err
	}
	return prov.Reinstall(ctx, cfg, hostID, osID)
}

// OSOptions 可用操作系统列表。
func (c *Console) OSOptions(ctx context.Context, userID, serviceID int64) ([]server.OSOption, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	return prov.ReinstallOptions(ctx, cfg, hostID)
}

// VNCInfo 返回结构化 VNC 会话信息（密码 + 上游 wss + 静态资源源站），供服务端反向代理。
func (c *Console) VNCInfo(ctx context.Context, userID, serviceID int64) (server.VNCInfo, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.VNCInfo{}, err
	}
	if vip, ok := prov.(server.VNCInfoProvider); ok {
		return vip.VNCInfo(ctx, cfg, hostID)
	}
	return server.VNCInfo{}, server.ErrNotSupported
}

// InvalidateVNC 清除 VNC 会话缓存（拨号 bad handshake 时强制下次拿新会话）。
func (c *Console) InvalidateVNC(ctx context.Context, userID, serviceID int64) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return
	}
	if iv, ok := prov.(interface {
		InvalidateVNC(ctx context.Context, cfg server.Config, upstreamHostID int64)
	}); ok {
		iv.InvalidateVNC(ctx, cfg, hostID)
	}
}

// Usage 流量用量；无数据返回空。
func (c *Console) Usage(ctx context.Context, userID, serviceID int64) (server.UsageInfo, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.UsageInfo{}, err
	}
	return prov.Usage(ctx, cfg, hostID)
}

// HostDetail 实时拉取实例登录与系统信息（供详情页展示，不落库）。
func (c *Console) HostDetail(ctx context.Context, userID, serviceID int64) (server.HostDetail, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.HostDetail{}, err
	}
	hdf, ok := prov.(server.HostDetailFetcher)
	if !ok {
		return server.HostDetail{}, server.ErrNotSupported
	}
	return hdf.HostDetail(ctx, cfg, hostID)
}

// Overview 一次性拉取详情页概况：登录/系统信息 + 模块清单（对 /host/header 仅一次请求）。
func (c *Console) Overview(ctx context.Context, userID, serviceID int64) (server.HostOverview, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.HostOverview{}, err
	}
	of, ok := prov.(server.HostOverviewFetcher)
	if !ok {
		return server.HostOverview{}, server.ErrNotSupported
	}
	return of.HostOverview(ctx, cfg, hostID)
}

// Chart 拉取监控图表时序（上游不支持时返回 ErrNotSupported）。
func (c *Console) Chart(ctx context.Context, userID, serviceID int64, typ, sel string) (server.ChartSeries, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.ChartSeries{}, err
	}
	cf, ok := prov.(server.ChartFetcher)
	if !ok {
		return server.ChartSeries{}, server.ErrNotSupported
	}
	return cf.Chart(ctx, cfg, hostID, typ, sel)
}

// PowerStatus 实时电源四态。
func (c *Console) PowerStatus(ctx context.Context, userID, serviceID int64) (server.PowerStatus, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.PowerStatus{}, err
	}
	pf, ok := prov.(server.PowerStatusFetcher)
	if !ok {
		return server.PowerStatus{}, server.ErrNotSupported
	}
	return pf.PowerStatus(ctx, cfg, hostID)
}

// TrafficUsage 每日流量曲线。
func (c *Console) TrafficUsage(ctx context.Context, userID, serviceID int64) ([]server.TrafficDay, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	tf, ok := prov.(server.TrafficUsageFetcher)
	if !ok {
		return nil, server.ErrNotSupported
	}
	return tf.TrafficUsage(ctx, cfg, hostID)
}

// SnapshotInfo 快照/备份概况（配额+列表+磁盘）。
func (c *Console) SnapshotInfo(ctx context.Context, userID, serviceID int64) (server.SnapshotInfo, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return server.SnapshotInfo{}, err
	}
	sp, ok := prov.(server.SnapshotProvider)
	if !ok {
		return server.SnapshotInfo{}, server.ErrNotSupported
	}
	return sp.SnapshotInfo(ctx, cfg, hostID)
}

// SnapshotAction 执行快照/备份操作。
func (c *Console) SnapshotAction(ctx context.Context, userID, serviceID int64, fn string, form url.Values) (string, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	sp, ok := prov.(server.SnapshotProvider)
	if !ok {
		return "", server.ErrNotSupported
	}
	return sp.SnapshotAction(ctx, cfg, hostID, fn, form)
}

// blocksProvider 解析方块能力提供方。
func (c *Console) blocksProvider(ctx context.Context, userID, serviceID int64) (server.ModuleBlocksProvider, server.Config, int64, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return nil, server.Config{}, 0, err
	}
	bp, ok := prov.(server.ModuleBlocksProvider)
	if !ok {
		return nil, server.Config{}, 0, server.ErrNotSupported
	}
	return bp, cfg, hostID, nil
}

// NatList NAT 转发列表。
func (c *Console) NatList(ctx context.Context, userID, serviceID int64) ([]server.NatRule, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	return bp.NatList(ctx, cfg, hostID)
}

// NatWebList 共享建站列表。
func (c *Console) NatWebList(ctx context.Context, userID, serviceID int64) ([]server.NatWebEntry, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	return bp.NatWebList(ctx, cfg, hostID)
}

// SecurityGroups 安全组列表。
func (c *Console) SecurityGroups(ctx context.Context, userID, serviceID int64) ([]server.SecurityGroup, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	return bp.SecurityGroups(ctx, cfg, hostID)
}

// SecurityRules 某安全组的规则列表。
func (c *Console) SecurityRules(ctx context.Context, userID, serviceID, groupID int64) ([]server.SecurityRule, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return nil, err
	}
	return bp.SecurityRules(ctx, cfg, hostID, groupID)
}

// SettingData 设置方块数据（ISO/启动顺序）。
func (c *Console) SettingData(ctx context.Context, userID, serviceID int64) (server.SettingData, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return server.SettingData{}, err
	}
	return bp.SettingData(ctx, cfg, hostID)
}

// BlockAction 执行方块操作。
func (c *Console) BlockAction(ctx context.Context, userID, serviceID int64, fn string, form url.Values) (string, error) {
	bp, cfg, hostID, err := c.blocksProvider(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	return bp.BlockAction(ctx, cfg, hostID, fn, form)
}

// moduleProvider 解析并返回实现了 ModuleProvider 的上游提供方。
func (c *Console) moduleProvider(ctx context.Context, userID, serviceID int64) (server.ModuleProvider, server.Config, int64, error) {
	prov, cfg, hostID, err := c.resolve(ctx, userID, serviceID)
	if err != nil {
		return nil, server.Config{}, 0, err
	}
	mp, ok := prov.(server.ModuleProvider)
	if !ok {
		return nil, server.Config{}, 0, server.ErrNotSupported
	}
	return mp, cfg, hostID, nil
}

// ModuleSummary 详情页"产品模块"清单（魔方云方块/按钮/图表开关）。
func (c *Console) ModuleSummary(ctx context.Context, userID, serviceID int64) (server.ModuleSummary, error) {
	mp, cfg, hostID, err := c.moduleProvider(ctx, userID, serviceID)
	if err != nil {
		return server.ModuleSummary{}, err
	}
	return mp.ModuleSummary(ctx, cfg, hostID)
}

// ModulePageContent 拉取某方块页面 HTML（本地代理内嵌，隐藏上游域名）。
func (c *Console) ModulePageContent(ctx context.Context, userID, serviceID int64, key string) (string, error) {
	mp, cfg, hostID, err := c.moduleProvider(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	return mp.ModulePage(ctx, cfg, hostID, key)
}

// ModuleAction 提交方块表单到上游，返回上游响应原文。
func (c *Console) ModuleAction(ctx context.Context, userID, serviceID int64, form url.Values) (string, error) {
	mp, cfg, hostID, err := c.moduleProvider(ctx, userID, serviceID)
	if err != nil {
		return "", err
	}
	return mp.ModuleAction(ctx, cfg, hostID, form)
}
