package cron

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"lumeidc/internal/service"
)

type Jobs struct {
	DB          *sql.DB
	Lifecycle   *service.Lifecycle
	Fulfillment *service.Fulfillment
	Notifier    *service.Notifier
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
	c.AddFunc("@every 10m", func() { j.suspendExpired(context.Background()) })
	c.AddFunc("@every 1h", func() { j.terminateSuspended(context.Background()) })
	c.AddFunc("@every 10m", func() { j.releaseExpiredStock(context.Background()) })
	// 到期前 3 天提醒（每 6 小时一次，避免重复发送由 expire_warn_sent 标记保证）
	c.AddFunc("@every 6h", func() { j.notifyExpiringSoon(context.Background()) })
	// 按上游同步本地服务状态（@every 30s），保证本地状态跟随上游真实状态
	c.AddFunc("@every 30s", func() {
		if j.Lifecycle != nil {
			j.Lifecycle.SyncUpstreamStatus(context.Background())
		}
	})
	c.Start()
	return c
}

// suspendExpired 到期服务 -> 停机（宽限期内可续费恢复），并同步上游
func (j *Jobs) suspendExpired(ctx context.Context) {
	rows, err := j.DB.QueryContext(ctx,
		`SELECT id FROM services WHERE status=1 AND expires_at < now()`)
	if err != nil {
		log.Printf("[cron] 查询到期服务失败: %v", err)
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
		if j.Lifecycle != nil {
			if err := j.Lifecycle.Suspend(ctx, id); err != nil {
				log.Printf("[cron] 服务 %d 停机失败: %v", id, err)
				continue
			}
			log.Printf("[cron] 服务 %d 已停机", id)
		}
	}
}

// terminateSuspended 停机超过 30 天的服务 -> 终止删除，并同步上游
func (j *Jobs) terminateSuspended(ctx context.Context) {
	rows, err := j.DB.QueryContext(ctx,
		`SELECT id FROM services WHERE status=2 AND expires_at < now() - interval '30 days'`)
	if err != nil {
		log.Printf("[cron] 删除超期停机服务失败: %v", err)
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
		if j.Lifecycle != nil {
			if err := j.Lifecycle.Terminate(ctx, id); err != nil {
				log.Printf("[cron] 服务 %d 删除失败: %v", id, err)
				continue
			}
			log.Printf("[cron] 服务 %d 已删除", id)
		}
	}
}

var _ = time.Now // 保留 time 以便后续宽限提醒任务

// notifyExpiringSoon 服务到期前 3 天向用户发送提醒（站内信+邮件），每个服务仅提醒一次。
func (j *Jobs) notifyExpiringSoon(ctx context.Context) {
	if j.Notifier == nil {
		return
	}
	rows, err := j.DB.QueryContext(ctx,
		`SELECT sv.id, sv.user_id, u.email, sv.expires_at
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
