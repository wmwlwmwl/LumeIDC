package dailyreport

import (
	"strings"
	"testing"

	"lumeidc/internal/plugin"
)

func TestSummaryText(t *testing.T) {
	s := summary{Orders: 3, PaidAmount: 199.5, NewUsers: 2, NewTickets: 1, OpenTickets: 5, Expiring30d: 4, ActiveService: 20}
	text := s.text()
	for _, want := range []string{"3 笔", "199.50 元", "2 人", "1 张", "待处理 5 张", "激活中服务 20 个", "4 个将在 30 天内到期"} {
		if !strings.Contains(text, want) {
			t.Fatalf("日报正文缺少 %q: %s", want, text)
		}
	}
}

// CronJobs：hour 配置缺省 8 点（Settings 为 nil 时 Host.Config 返回空串）。
func TestCronJobSpec(t *testing.T) {
	p := &Plugin{host: &plugin.Host{}}
	jobs := p.CronJobs()
	if len(jobs) != 1 || jobs[0].Spec != "0 8 * * *" {
		t.Fatalf("缺省 spec 应为 0 8 * * *: %+v", jobs)
	}
	if jobs[0].What == "" || jobs[0].Run == nil {
		t.Fatal("任务应带中文名与执行函数")
	}
}
