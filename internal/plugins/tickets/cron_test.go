package tickets

import (
	"context"
	"testing"

	"lumeidc/internal/plugin"
)

// 超时提醒：通知入队失败时不应标记（标记了就会吞提醒）。
// SQL 行为由核心迁移与集成环境保障；这里锁「失败不标记」的判定语义。
func TestNotifyStaleTicketsNoNotifyNoMark(t *testing.T) {
	// 无 DB 时直接覆盖分支语义：Notifier 缺失时函数应安静返回（不报错、不标记）。
	p := &Plugin{host: &plugin.Host{}}
	if err := p.notifyStaleTickets(context.Background()); err != nil {
		t.Fatalf("Notifier 缺失时应静默返回: %v", err)
	}
}

// ticketNotify：Notifier 缺失不 panic；模板缺省值兜底。
func TestTicketNotifyNilNotifier(t *testing.T) {
	p := &Plugin{host: &plugin.Host{}}
	p.ticketNotify(context.Background(), 1, "created", "测试工单") // 不 panic 即通过
}
