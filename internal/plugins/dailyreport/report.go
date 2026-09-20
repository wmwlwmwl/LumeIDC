package dailyreport

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/plugin"
)

// CronJobs 每日报告任务。Spec 按配置的发送时刻生成；
// 配置修改需重启生效（cron 注册发生在启动期）。
func (p *Plugin) CronJobs() []plugin.CronJob {
	hour := 8
	if v := p.host.Config(context.Background(), "hour"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h < 24 {
			hour = h
		}
	}
	return []plugin.CronJob{{
		Name: "daily_report",
		Spec: fmt.Sprintf("0 %d * * *", hour),
		What: "每日运营报告",
		Run:  p.run,
	}}
}

// summary 昨日经营汇总（一条 SQL 取全部计数，避免多次往返）。
type summary struct {
	Orders        int64   // 昨日新订单
	PaidAmount    float64 // 昨日实收
	NewUsers      int64   // 昨日新注册
	NewTickets    int64   // 昨日新工单
	OpenTickets   int64   // 当前待处理工单
	Expiring30d   int64   // 30 天内到期服务
	ActiveService int64   // 激活中服务
}

func (p *Plugin) collect(ctx context.Context) (summary, error) {
	var s summary
	err := p.host.DB.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM orders WHERE created_at >= CURRENT_DATE - 1 AND created_at < CURRENT_DATE),
		  (SELECT coalesce(sum(amount),0)::float8 FROM invoices WHERE status=1 AND paid_at >= CURRENT_DATE - 1 AND paid_at < CURRENT_DATE),
		  (SELECT count(*) FROM users WHERE created_at >= CURRENT_DATE - 1 AND created_at < CURRENT_DATE),
		  (SELECT count(*) FROM tickets WHERE created_at >= CURRENT_DATE - 1 AND created_at < CURRENT_DATE),
		  (SELECT count(*) FROM tickets WHERE status <> 'closed'),
		  (SELECT count(*) FROM services WHERE status = 1 AND expires_at < now() + interval '30 days'),
		  (SELECT count(*) FROM services WHERE status = 1)
	`).Scan(&s.Orders, &s.PaidAmount, &s.NewUsers, &s.NewTickets, &s.OpenTickets, &s.Expiring30d, &s.ActiveService)
	return s, err
}

func (s summary) text() string {
	return fmt.Sprintf(
		"昨日新订单 %d 笔，实收 %.2f 元；新注册用户 %d 人；新工单 %d 张（当前待处理 %d 张）。激活中服务 %d 个，其中 %d 个将在 30 天内到期。",
		s.Orders, s.PaidAmount, s.NewUsers, s.NewTickets, s.OpenTickets, s.ActiveService, s.Expiring30d,
	)
}

// run 生成并推送日报（NotifyAdminOnce 按日期 key 去重，重复执行/多实例只发一次）。
func (p *Plugin) run(ctx context.Context) error {
	if p.host.Config(ctx, "enabled") != "1" {
		return nil
	}
	if p.host.Notify == nil {
		return fmt.Errorf("通知服务未注入")
	}
	s, err := p.collect(ctx)
	if err != nil {
		return fmt.Errorf("汇总昨日经营数据失败: %w", err)
	}
	day := time.Now().Format("2006-01-02")
	return p.host.Notify.NotifyAdminOnce(ctx, "daily_report:"+day, "运营日报",
		"每日运营报告（"+day+"）", s.text())
}

// sendNow POST /admin/plugin/dailyreport/send-now — 立即发送一次（联调用；按日 key 幂等）。
func (p *Plugin) sendNow(w http.ResponseWriter, r *http.Request) {
	if !plugin.AdminOK(w, r) {
		return
	}
	if err := p.run(r.Context()); err != nil {
		plugin.JSONFail(w, "发送失败，请稍后重试")
		return
	}
	plugin.WriteJSON(w, map[string]any{"ok": 1, "msg": "已发送（今日已发过则不重复）"})
}
