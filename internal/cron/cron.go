package cron

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type Jobs struct {
	DB          *sql.DB
	Lifecycle   *service.Lifecycle
	Fulfillment *service.Fulfillment
	Notifier    *service.Notifier
	Providers   *server.Registry
	Servers     *repo.Servers
	Products    *repo.Products
}

func (j *Jobs) Start() *cron.Cron {
	c := cron.New(cron.WithChain(
		cron.SkipIfStillRunning(cron.DefaultLogger),
	))
	c.AddFunc("@every 15s", func() {
		if j.Fulfillment != nil {
			j.Fulfillment.Drain(context.Background(), 10)
		}
	})
	c.AddFunc("@every 10m", func() {
		j.runExpired(context.Background(),
			`SELECT id FROM services WHERE status=1 AND expires_at < now()`, "停机",
			func(ctx context.Context, id int64) error { return j.Lifecycle.Suspend(ctx, id) })
	})
	c.AddFunc("@every 1h", func() {
		j.runExpired(context.Background(),
			`SELECT id FROM services WHERE status=2 AND expires_at < now() - interval '30 days'`, "删除",
			func(ctx context.Context, id int64) error { return j.Lifecycle.Terminate(ctx, id) })
	})
	c.AddFunc("@every 10m", func() { j.releaseExpiredStock(context.Background()) })
	// 到期前 3 天提醒（每 6 小时一次，避免重复发送由 expire_warn_sent 标记保证）
	c.AddFunc("@every 6h", func() { j.notifyExpiringSoon(context.Background()) })
	// 按上游同步本地服务状态（@every 30s），保证本地状态跟随上游真实状态
	c.AddFunc("@every 30s", func() {
		if j.Lifecycle != nil {
			j.Lifecycle.SyncUpstreamStatus(context.Background())
		}
	})
	// 定时同步上游产品价格与库存（每 6 小时）
	c.AddFunc("@every 6h", func() { j.syncPrices(context.Background()) })
	c.Start()
	return c
}

// runExpired 对满足条件的到期服务批量执行生命周期操作（停机/删除共用）。
// query 返回待处理服务 id；opName 用于日志（如 "停机"/"删除"）；op 为对单个服务的操作。
func (j *Jobs) runExpired(ctx context.Context, query, opName string, op func(context.Context, int64) error) {
	if j.Lifecycle == nil {
		return
	}
	rows, err := j.DB.QueryContext(ctx, query)
	if err != nil {
		log.Printf("[cron] 查询待%s服务失败: %v", opName, err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			log.Printf("[cron] 读取到期服务失败: %v", err)
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[cron] 遍历到期服务失败: %v", err)
	}
	rows.Close()
	for _, id := range ids {
		if err := op(ctx, id); err != nil {
			log.Printf("[cron] 服务 %d %s失败: %v", id, opName, err)
			continue
		}
		log.Printf("[cron] 服务 %d 已%s", id, opName)
	}
}

// notifyExpiringSoon 服务到期前 3 天向用户发送提醒（站内信+邮件），每个服务仅提醒一次。
func (j *Jobs) notifyExpiringSoon(ctx context.Context) {
	if j.Notifier == nil {
		return
	}
	rows, err := j.DB.QueryContext(ctx,
		`SELECT sv.id, sv.user_id, coalesce(u.email,''), sv.expires_at
		 FROM services sv JOIN users u ON u.id=sv.user_id
		 WHERE sv.status=1 AND sv.expire_warn_sent=false
		   AND sv.expires_at BETWEEN now() AND now() + interval '3 days'`)
	if err != nil {
		log.Printf("[cron] 查询即将到期服务失败: %v", err)
		return
	}
	type item struct {
		id  int64
		uid int64
		exp time.Time
	}
	var items []item
	for rows.Next() {
		var it item
		var email string
		if err := rows.Scan(&it.id, &it.uid, &email, &it.exp); err != nil {
			continue
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		body := fmt.Sprintf("您的服务将于 %s 到期，请及时续费以免停机。", it.exp.Format("2006-01-02 15:04"))
		j.Notifier.Notify(ctx, it.uid, "服务即将到期", body)
		if _, err := j.DB.ExecContext(ctx,
			`UPDATE services SET expire_warn_sent=true WHERE id=$1`, it.id); err != nil {
			log.Printf("[cron] 更新到期提醒标记失败 service=%d: %v", it.id, err)
		}
	}
}

// releaseExpiredStock 释放过期的库存预留（未支付订单超时）。
func (j *Jobs) releaseExpiredStock(ctx context.Context) {
	res, err := j.DB.ExecContext(ctx,
		`UPDATE stock_reservations SET status='released'
		 WHERE status='reserved' AND expires_at < now()`)
	if err != nil {
		log.Printf("[cron] 释放过期库存预留失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[cron] 已释放 %d 条过期库存预留", n)
	}
}

// syncPrices 定时同步上游产品价格与库存（按服务器分组，每服务器一次 Catalog 调用）。
func (j *Jobs) syncPrices(ctx context.Context) {
	if j.Providers == nil || j.Servers == nil || j.Products == nil {
		return
	}
	bound, err := j.Products.ListBound(ctx)
	if err != nil {
		log.Printf("[sync] 查询已绑定产品失败: %v", err)
		return
	}
	if len(bound) == 0 {
		return
	}
	// 按 server_id 分组
	type group struct {
		sv   *repo.Server
		pids map[int64]int64 // upstream_pid -> local product_id
	}
	groups := map[int64]*group{}
	for _, bp := range bound {
		g, ok := groups[bp.ServerID]
		if !ok {
			sv, serr := j.Servers.Get(ctx, bp.ServerID)
			if serr != nil {
				log.Printf("[sync] 读取服务器 %d 失败: %v", bp.ServerID, serr)
				continue
			}
			g = &group{sv: sv, pids: map[int64]int64{}}
			groups[bp.ServerID] = g
		}
		g.pids[bp.UpstreamPID] = bp.ID
	}
	updated, failed := 0, 0
	for _, g := range groups {
		prov, err := j.Providers.Get(g.sv.Provider)
		if err != nil {
			log.Printf("[sync] 供应商 %s 不可用: %v", g.sv.Provider, err)
			continue
		}
		cfg := server.Config{APIURL: g.sv.APIURL, APIUsername: g.sv.APIUsername, APIKey: g.sv.APIKey}
		list, err := prov.Catalog(ctx, cfg)
		if err != nil {
			log.Printf("[sync] 拉取 %s 目录失败: %v", g.sv.Name, err)
			continue
		}
		for _, up := range list {
			pid, ok := g.pids[int64(up.PID)]
			if !ok {
				continue
			}
			if err := j.Products.UpdatePriceAndStock(ctx, pid, up.Monthly, up.Quarterly, up.Yearly, up.Stock); err != nil {
				log.Printf("[sync] 更新产品 %d 失败: %v", pid, err)
				failed++
				continue
			}
			updated++
		}
	}
	if updated > 0 || failed > 0 {
		log.Printf("[sync] 价格/库存同步完成: 更新 %d，失败 %d", updated, failed)
	}
}
