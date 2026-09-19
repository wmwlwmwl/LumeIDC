package tickets

import (
	"context"
	"strings"

	"lumeidc/internal/plugin"
)

// ticketNotify 按 settings 中的工单通知模板给用户发站内信/邮件
// （模板保留核心通知系统管理，本插件只触发发送）。原 service.Notifier.TicketNotify。
func (p *Plugin) ticketNotify(ctx context.Context, userID int64, event, subject string) {
	if p.host.Notify == nil || p.host.Settings == nil {
		return
	}
	title, _ := p.host.Settings.Get(ctx, "ticket_notify_"+event+"_title")
	body, _ := p.host.Settings.Get(ctx, "ticket_notify_"+event+"_body")
	if strings.TrimSpace(title) == "" {
		title = "工单通知"
	}
	if strings.TrimSpace(body) == "" {
		body = "你的工单「{{subject}}」有新的处理动态。"
	}
	body = strings.ReplaceAll(body, "{{subject}}", subject)
	_ = p.host.Notify.NotifyTemplate(ctx, userID, "ticket_"+event, title, body, map[string]string{"subject": subject})
}

// CronJobs 超时工单提醒（原 cron.notifyStaleTickets，逻辑原样搬迁）。
func (p *Plugin) CronJobs() []plugin.CronJob {
	return []plugin.CronJob{{
		Name: "notify_stale_tickets",
		Spec: "@every 1h",
		What: "查询超时工单",
		Run:  p.notifyStaleTickets,
	}}
}

// notifyStaleTickets 对超过 24 小时未更新的未关闭工单给用户发提醒，每单 24h 最多一次。
func (p *Plugin) notifyStaleTickets(ctx context.Context) error {
	if p.host.Notify == nil {
		return nil
	}
	rows, err := p.host.DB.QueryContext(ctx, `SELECT id,user_id,subject FROM tickets WHERE status<>'closed' AND updated_at < now()-interval '24 hours' AND (timeout_notified_at IS NULL OR timeout_notified_at < now()-interval '24 hours')`)
	if err != nil {
		return err
	}
	type ticket struct {
		id, userID int64
		subject    string
	}
	var list []ticket
	for rows.Next() {
		var t ticket
		if err := rows.Scan(&t.id, &t.userID, &t.subject); err != nil {
			rows.Close()
			return err
		}
		list = append(list, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	title, _ := p.host.Settings.Get(ctx, "ticket_notify_timeout_title")
	if strings.TrimSpace(title) == "" {
		title = "工单处理提醒"
	}
	var notified []int64
	for _, t := range list {
		body := "你的工单「" + t.subject + "」仍在处理中，客服会尽快跟进。"
		if err := p.host.Notify.NotifyTemplate(ctx, t.userID, "ticket_timeout", title, body, map[string]string{"subject": t.subject}); err != nil {
			continue
		}
		notified = append(notified, t.id)
	}
	// 仅标记已可靠入队的通知；提交后标记前崩溃可能重复提醒，但不会吞通知。
	if len(notified) > 0 {
		if _, err := p.host.DB.ExecContext(ctx, `UPDATE tickets SET timeout_notified_at=now() WHERE id = ANY($1)`, notified); err != nil {
			return err
		}
	}
	return nil
}
