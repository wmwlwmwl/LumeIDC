package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// Lifecycle 服务生命周期操作（接上游）。
type Lifecycle struct {
	db        *sql.DB
	Servers   *repo.Servers
	Products  *repo.Products
	Providers *server.Registry
}

type serviceRef struct {
	ID               int64
	Status           int16
	ServerID         sql.NullInt64
	UpstreamPID      int64
	UpstreamProvider string
	UpstreamHost     int64
	UpstreamCycle    string
}

// loadService 读服务+产品绑定信息。
func (lc *Lifecycle) loadService(ctx context.Context, serviceID int64) (*serviceRef, error) {
	var s serviceRef
	err := lc.db.QueryRowContext(ctx,
		`SELECT sv.id,sv.status,coalesce(sv.server_id,p.server_id),
		        coalesce(nullif(sv.upstream_pid,0),p.upstream_pid),
		        coalesce(nullif(sv.upstream_provider,''),srv.provider,''),
		        sv.upstream_host_id,coalesce(p.upstream_cycle,'')
		 FROM services sv JOIN products p ON p.id=sv.product_id
		 LEFT JOIN servers srv ON srv.id=coalesce(sv.server_id,p.server_id)
		 WHERE sv.id=$1`,
		serviceID).Scan(&s.ID, &s.Status, &s.ServerID, &s.UpstreamPID, &s.UpstreamProvider,
		&s.UpstreamHost, &s.UpstreamCycle)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// resolveProvider 按供应商代码+服务器ID 解析上游 Provider 与 Config（lifecycle/console 共用）。
func resolveProvider(ctx context.Context, providers *server.Registry, servers *repo.Servers, providerCode string, serverID int64) (server.Provider, server.Config, error) {
	prov, err := providers.Get(providerCode)
	if err != nil {
		return nil, server.Config{}, err
	}
	sv, err := servers.Get(ctx, serverID)
	if err != nil {
		return nil, server.Config{}, fmt.Errorf("读取服务器失败: %w", err)
	}
	return prov, upstreamConfig(sv), nil
}

func (lc *Lifecycle) providerFor(ctx context.Context, s *serviceRef) (server.Provider, server.Config, error) {
	// 同 console.resolve：hostID>0 即有上游；upstream_pid=0 为合法弹性模式（EasyPanel）。
	if !s.ServerID.Valid || s.UpstreamHost == 0 {
		return nil, server.Config{}, errNoUpstream
	}
	return resolveProvider(ctx, lc.Providers, lc.Servers, s.UpstreamProvider, s.ServerID.Int64)
}

var errNoUpstream = lifecycleErr("该服务未绑定上游")

type lifecycleErr string

func (e lifecycleErr) Error() string { return string(e) }

func upstreamConfig(sv *repo.Server) server.Config {
	return server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
}

const opTimeout = 60 * time.Second

// Renew 向上游续费。
// 使用 checkpoint 记录续费结果，避免重试时重复创建续费单。
func (lc *Lifecycle) Renew(ctx context.Context, serviceID int64, cycle string, orderID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	prov, cfg, err := lc.providerFor(ctx, s)
	if err == errNoUpstream {
		return nil // 本地服务，无需上游操作
	}
	if err != nil {
		return err
	}
	checkpointKey := fmt.Sprintf("renew_order_%d", orderID)
	if orderID <= 0 {
		return fmt.Errorf("续费任务缺少订单号")
	}
	if existing, ok, _ := lc.getCheckpoint(ctx, serviceID, checkpointKey); ok && existing != "" {
		log.Printf("[lifecycle] service %d 已有续费 checkpoint，跳过: %s", serviceID, existing)
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := prov.Renew(cctx, cfg, s.UpstreamHost, cycle); err != nil {
		log.Printf("[lifecycle] service %d 上游续费失败: %v", serviceID, err)
		return fmt.Errorf("上游续费失败: %w", err)
	}
	// 续费成功，写入 checkpoint
	if err := lc.setCheckpoint(ctx, serviceID, checkpointKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("[lifecycle] service %d 写入续费 checkpoint 失败: %v", serviceID, err)
	}
	return nil
}

// getCheckpoint 从 services.provision_data 读取 checkpoint。
func (lc *Lifecycle) getCheckpoint(ctx context.Context, serviceID int64, key string) (string, bool, error) {
	var data []byte
	if err := lc.db.QueryRowContext(ctx,
		`SELECT provision_data FROM services WHERE id=$1`, serviceID).Scan(&data); err != nil {
		return "", false, err
	}
	if len(data) == 0 {
		return "", false, nil
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return "", false, nil
	}
	v, ok := m[key]
	return v, ok, nil
}

// setCheckpoint 写入 checkpoint 到 services.provision_data。
func (lc *Lifecycle) setCheckpoint(ctx context.Context, serviceID int64, key, val string) error {
	_, err := lc.db.ExecContext(ctx,
		`UPDATE services SET provision_data = coalesce(provision_data,'{}')::jsonb || $2::jsonb WHERE id=$1`,
		serviceID, fmt.Sprintf(`{"%s":"%s"}`, key, val))
	return err
}

// transition 服务状态迁移通用流程（停机/解停/删除共用）。
// from/to 为状态迁移边界，cmp 为本地状态条件比较符（"=" 或 "<"，Terminate 用 status<3 表达"未终止皆可删"）；
// state 为过渡状态名；errMsg 为上游失败时的日志与包装文案；op 为对上游的实际操作。
func (lc *Lifecycle) transition(ctx context.Context, s *serviceRef, from, to int16, cmp, state, errMsg string, op func(context.Context, server.Provider, server.Config, int64) error) error {
	prov, cfg, err := lc.providerFor(ctx, s)
	if err == errNoUpstream {
		_, err = lc.db.ExecContext(ctx,
			`UPDATE services SET status=$1 WHERE id=$2 AND status`+cmp+`$3`, to, s.ID, from)
		return err
	}
	if err != nil {
		return err
	}
	// 设置过渡状态
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET desired_status=$1, transition_state=$4 WHERE id=$2 AND status`+cmp+`$3`,
		to, s.ID, from, state); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := op(cctx, prov, cfg, s.UpstreamHost); err != nil {
		log.Printf("[lifecycle] service %d %s: %v", s.ID, errMsg, err)
		// 失败：清除过渡状态，保留原状态
		lc.db.ExecContext(ctx, `UPDATE services SET desired_status=NULL, transition_state='' WHERE id=$1`, s.ID)
		return fmt.Errorf("%s: %w", errMsg, err)
	}
	// 成功：更新状态
	_, err = lc.db.ExecContext(ctx,
		`UPDATE services SET status=$1 WHERE id=$2 AND status`+cmp+`$3`, to, s.ID, from)
	// 无条件清除过渡状态：即便并发的 SyncUpstreamStatus 改了 status，
	// 也不让其卡在 transition_state（否则会被同步长期跳过）。
	if _, e := lc.db.ExecContext(ctx,
		`UPDATE services SET desired_status=NULL, transition_state='' WHERE id=$1`, s.ID); e != nil {
		log.Printf("[lifecycle] service %d 清除过渡状态失败: %v", s.ID, e)
	}
	return err
}

// Suspend 停机：本地状态 + 上游同步。
func (lc *Lifecycle) Suspend(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status != 1 {
		return fmt.Errorf("服务当前状态不可停机")
	}
	return lc.transition(ctx, s, 1, 2, "=", "suspending", "上游停机失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Suspend(ctx, cfg, host)
		})
}

// Unsuspend 解除停机。
func (lc *Lifecycle) Unsuspend(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status != 2 {
		return fmt.Errorf("服务当前状态不可解除停机")
	}
	return lc.transition(ctx, s, 2, 1, "=", "unsuspending", "上游解除停机失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Unsuspend(ctx, cfg, host)
		})
}

// Terminate 删除：本地终止 + 上游销毁。
func (lc *Lifecycle) Terminate(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status == 3 {
		return nil
	}
	return lc.transition(ctx, s, 3, 3, "<", "terminating", "上游删除失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Terminate(ctx, cfg, host)
		})
}

// RetryProvision 手动重试开通（pending 状态的服务）。
// 通过入队 fulfillment job 实现，不允许与同服务已有 running job 并行。
func (p *Payment) RetryProvision(ctx context.Context, serviceID int64) error {
	var cycle sql.NullString
	err := p.db.QueryRowContext(ctx,
		`SELECT (SELECT cycle FROM orders WHERE id=services.order_id) FROM services WHERE id=$1 AND status=0`,
		serviceID).Scan(&cycle)
	if err != nil {
		return fmt.Errorf("服务不存在或非待开通状态")
	}
	if p.Jobs == nil {
		// 无 job 系统时回退到直接调用
		var productID int64
		if err := p.db.QueryRowContext(ctx,
			`SELECT product_id FROM services WHERE id=$1`, serviceID).Scan(&productID); err != nil {
			return err
		}
		return p.provision(ctx, serviceID, productID, cycle.String)
	}
	running, err := p.Jobs.HasRunningJob(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("检查任务状态失败: %w", err)
	}
	if running {
		return fmt.Errorf("该服务已有正在执行的任务，请等待完成后再试")
	}
	return p.Jobs.EnqueueRetry(ctx, serviceID, "provision", cycle.String)
}

// SyncUpstreamStatus 将本地服务状态同步为上游真实状态（按上游）。
// ponytail: 仅覆盖已绑定上游 host 的服务；查询密集度=服务数/周期，规模大时建议增量+过期过滤。
// 跳过过渡中（transition_state!=”）和已终止（status=3）的服务，避免复活或干扰进行中的操作。
// 使用 LIMIT 分页避免一次加载过多服务；未知上游状态只记录不覆盖本地。
func (lc *Lifecycle) SyncUpstreamStatus(ctx context.Context) {
	// 同步租约：防止并发同步
	leaseKey := "sync_upstream_status"
	var gotLease bool
	if err := lc.db.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock(hashtext($1))`, leaseKey).Scan(&gotLease); err != nil || !gotLease {
		return
	}
	defer lc.db.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, leaseKey)

	const pageSize = 100
	offset := 0
	for {
		rows, err := lc.db.QueryContext(ctx,
			`SELECT sv.id, srv.api_url, srv.api_username, srv.api_key, sv.upstream_host_id,
			        coalesce(nullif(sv.upstream_provider,''),srv.provider,'')
			 FROM services sv
			 JOIN servers srv ON srv.id=sv.server_id
			 WHERE sv.upstream_host_id > 0 AND sv.status IN (0,1,2) AND sv.transition_state = ''
			 ORDER BY sv.id LIMIT $1 OFFSET $2`, pageSize, offset)
		if err != nil {
			log.Printf("[sync] 查询上游服务失败: %v", err)
			return
		}
		defer rows.Close()
		type row struct {
			id       int64
			cfg      server.Config
			host     int64
			provider string
		}
		var list []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.cfg.APIURL, &r.cfg.APIUsername, &r.cfg.APIKey, &r.host, &r.provider); err != nil {
				log.Printf("[sync] 读取服务失败: %v", err)
				continue
			}
			list = append(list, r)
		}
		if err := rows.Err(); err != nil {
			log.Printf("[sync] 遍历服务失败: %v", err)
			return
		}
		if len(list) == 0 {
			break
		}
		for _, r := range list {
			prov, err := lc.Providers.Get(r.provider)
			if err != nil {
				log.Printf("[sync] service %d 供应商错误: %v", r.id, err)
				continue
			}
			cc, cancel := context.WithTimeout(ctx, opTimeout)
			up, err := prov.Status(cc, r.cfg, r.host)
			cancel()
			if err != nil {
				log.Printf("[sync] service %d 状态查询失败: %v", r.id, err)
				continue
			}
			st, ok := mapUpstreamStatus(up.Status)
			if !ok {
				// 未知上游状态只记录不覆盖本地
				log.Printf("[sync] service %d 未知上游状态: %s", r.id, up.Status)
				continue
			}
			if _, err := lc.db.ExecContext(ctx,
				`UPDATE services SET status=$2, hostname=coalesce(nullif($3,''), hostname)
			 WHERE id=$1 AND status<3`, r.id, st, up.Hostname); err != nil {
				log.Printf("[sync] service %d 状态更新失败: %v", r.id, err)
			}
		}
		offset += len(list)
	}
}

// mapUpstreamStatus 上游 domainstatus -> 本地 status；未知状态返回 false 不改动。
func mapUpstreamStatus(u string) (int16, bool) {
	switch strings.ToLower(u) {
	case "active":
		return 1, true
	case "suspended":
		return 2, true
	case "terminated":
		return 3, true
	case "pending":
		return 0, true
	}
	return 0, false
}

// Upgrade 上游升降级钩子（预留）。
// ponytail: 一期本地为主，不改上游实例资源，本方法为 no-op 占位；
// 二期按 upstream_host_id 调魔方财务 /api/v1/hosts/:id/actions/upgrade 或
// /api/v1/hosts/:id/actions/upgradeconfig + /checkout 时在此实现。
func (lc *Lifecycle) Upgrade(ctx context.Context, serviceID, targetProductID int64, cycle string, orderID int64) error {
	return nil
}
