package easypanel

// EasyPanel 升降级：kangle EasyPanel 没有"换商品"的独立接口，
// 复用 add_vh 的 edit=1 形态——官方文档原文即"add_vh: 升级网站或者修改网站参数…
// 只是需要把 init=1 换成 edit=1 即可"。
//
// 2026-09-18 在真实 EP（2.6.29）上实测确认：
//   - edit=1 不带 uid 也能成功（源码 vhost.api.php 的 edit 分支会自行处理 uid）
//   - 改 web_quota 后 getVh 读回值确实变化（100 → 200），说明是真的改配置而非只返回成功
//   - 站点名必须为 u{数字} 一类合法名；含下划线的名字会被拒（result=500 且无 msg）

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// Upgrade a=add_vh&edit=1 修改站点配置（等价于升降级）。
//
// 两种模式：
//   - req.TargetPID>0：EP 产品模式，直接换 product_id（配额由 EP 面板产品定义）
//   - =0：弹性模式，按 flexibleFields 白名单透传 req.ConfigOpts 里的配额字段
//
// 失败不回滚本地：本地配置在 lifecycle.Upgrade 里是"上游成功后才落地"，
// 因此这里失败时本地仍是旧状态，重试是幂等的。
func (p Provider) Upgrade(ctx context.Context, cfg server.Config, upstreamHostID int64, req server.UpgradeRequest, _ server.CheckpointStore) error {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return err
	}
	c := newClient(cfg)
	name := SiteName(id)

	params := map[string]string{"edit": "1", "name": name}
	if req.TargetPID > 0 {
		params["product_id"] = strconv.FormatInt(req.TargetPID, 10)
	} else {
		// 弹性模式：透传配额字段，其余默认值与 Provision 保持一致，
		// 避免"升级后把没传的字段重置成空"。
		for _, f := range flexibleFields {
			if v, ok := req.ConfigOpts[f]; ok && strings.TrimSpace(v) != "" {
				params[f] = strings.TrimSpace(v)
			}
		}
		if _, ok := params["templete"]; !ok {
			params["templete"] = "easypanel"
		}
		if _, ok := params["module"]; !ok && params["templete"] == "easypanel" {
			params["module"] = "php"
		}
		if _, ok := params["subdir_flag"]; !ok {
			params["subdir_flag"] = "1"
		}
		if _, ok := params["ftp"]; !ok {
			params["ftp"] = "1"
		}
	}

	if _, err := c.call(ctx, "add_vh", params); err != nil {
		// 500 是 EP 的通用失败码，且不带 msg，实测两种成因都走它：
		//   1) 站点不存在（已在上游被删）
		//   2) 参数被拒（如 PID 模式传了上游没有的 product_id）——2026-09-18 实测确认
		// 因此文案不能只写"站点不存在"，否则会误导排查方向；但必须报错，
		// 绝不能让"付了钱、接口说成功、配置没变"发生。
		if code := apiCode(err); code == 500 || code == 404 {
			return fmt.Errorf("EasyPanel 升降级被拒（站点 %s 不存在，或参数无效如 product_id 有误）: %w", name, err)
		}
		return fmt.Errorf("EasyPanel 升降级失败: %w", err)
	}
	return nil
}
