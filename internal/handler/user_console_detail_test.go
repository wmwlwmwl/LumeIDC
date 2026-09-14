package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

func TestParseUpstreamExpiryIn(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	// 同一无时区串在不同时区下解释为不同绝对时刻
	sh, err := parseUpstreamExpiryIn("2026-09-20 12:00:00", cst)
	if err != nil {
		t.Fatal(err)
	}
	ut, err := parseUpstreamExpiryIn("2026-09-20 12:00:00", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// +8 解释的绝对时刻比 UTC 解释早 8 小时（同一墙上时钟，东边时区更早）
	if ut.Sub(sh) != 8*time.Hour {
		t.Fatalf("同一串两种时区解释差 = %v, want 8h", ut.Sub(sh))
	}
	// RFC3339 自带偏移，时区参数不影响结果
	if r, err := parseUpstreamExpiryIn("2026-09-20T12:00:00+08:00", time.UTC); err != nil || !r.Equal(sh) {
		t.Fatalf("RFC3339 解析 = %v, err=%v, want %v", r, err, sh)
	}
	// Unix 时间戳为绝对时刻，不受时区影响
	if u, err := parseUpstreamExpiryIn("1758331200", time.UTC); err != nil || u.Unix() != 1758331200 {
		t.Fatalf("unix 解析 = %v, err=%v", u, err)
	}
	// 毫秒时间戳
	if u, err := parseUpstreamExpiryIn("1758331200000", time.UTC); err != nil || u.Unix() != 1758331200 {
		t.Fatalf("毫秒解析 = %v, err=%v", u, err)
	}
	// 仅日期布局
	if d, err := parseUpstreamExpiryIn("2026-09-20", cst); err != nil || d.Format("2006-01-02") != "2026-09-20" {
		t.Fatalf("日期解析 = %v, err=%v", d, err)
	}
	if _, err := parseUpstreamExpiryIn("not-a-time", cst); err == nil {
		t.Fatal("非法输入应返回错误")
	}
}

// 复用产品列表测试的 Rows；所有连接均为内存驱动，不读取真实数据库。
type serviceListDB struct {
	prodListDB
	n       int
	mixed   bool
	lookups atomic.Int32
}

func (d *serviceListDB) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (d *serviceListDB) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows := &prodListRows{}
	switch {
	case strings.Contains(query, "WHERE sv.user_id=$1 AND sv.status<3"):
		rows.cols = make([]string, 21)
		for _, field := range []string{"IP", "OSName", "OSVersion"} {
			if !strings.Contains(query, "sv.host_snapshot->'Detail'->>'"+field+"'") {
				return nil, fmt.Errorf("缺少安全快照投影：%s", field)
			}
		}
		if strings.Count(query, "sv.host_snapshot") != 3 || !strings.Contains(query, "coalesce(sv.server_id,p.server_id) IS NOT NULL AND coalesce(sv.upstream_host_id,0)>0") {
			return nil, errors.New("列表应仅提取安全快照字段并检查绑定")
		}
		if args[0].Value != int64(7) {
			return rows, nil
		}
		for i := 1; i <= d.n; i++ {
			status, bound := int64(1), true
			if d.mixed && i == 2 {
				bound = false
			}
			if d.mixed && i == 3 {
				status = 0
			}
			if d.mixed && i == 4 {
				status = 2
			}
			row := []driver.Value{int64(i), "服务", status, time.Now().Add(24 * time.Hour), int64(10), "主机名", nil, "配置说明", "[]", "10", "0", "100", int64(0), float64(0), int64(0), float64(0), ""}
			row = append(row, bound, fmt.Sprintf("192.0.2.%d", i), "Debian", "12")
			rows.rows = append(rows.rows, row)
		}
	case strings.Contains(query, "SELECT sv.upstream_host_id"):
		d.lookups.Add(1)
		if !strings.Contains(query, "sv.user_id=$2 AND sv.status IN (1,2)") {
			return nil, errors.New("实时查询缺少归属或状态条件")
		}
		rows.cols = make([]string, 3)
		if args[1].Value == int64(7) {
			id := args[0].Value.(int64)
			if !(d.mixed && id == 3) {
				host := id
				if d.mixed && id == 2 {
					host = 0
				}
				rows.rows = [][]driver.Value{{host, int64(1), "列表测试"}}
			}
		}
	case strings.Contains(query, "FROM servers WHERE id=$1"):
		rows.cols = make([]string, 12)
		rows.rows = [][]driver.Value{{int64(1), "测试", "列表测试", "", "", "", false, int64(0), int64(0), float64(0), true, int64(10)}}
	default:
		return nil, fmt.Errorf("非预期列表查询：%s", query)
	}
	return rows, nil
}

type listRequestKey struct{}
type listConcurrency struct{ active, peak atomic.Int32 }

func (c *listConcurrency) enter() {
	n := c.active.Add(1)
	for old := c.peak.Load(); n > old; old = c.peak.Load() {
		if c.peak.CompareAndSwap(old, n) {
			break
		}
	}
}

type listProvider struct {
	server.Provider
	listConcurrency
	calls atomic.Int32
	fetch func(context.Context, int64) (server.HostDetail, error)
}

func (*listProvider) Code() string { return "列表测试" }
func (*listProvider) Name() string { return "列表测试" }
func (p *listProvider) HostDetail(ctx context.Context, _ server.Config, id int64) (server.HostDetail, error) {
	p.enter()
	defer p.active.Add(-1)
	if c, ok := ctx.Value(listRequestKey{}).(*listConcurrency); ok {
		c.enter()
		defer c.active.Add(-1)
	}
	p.calls.Add(1)
	return p.fetch(ctx, id)
}

func serviceListFixture(t *testing.T, n int, mixed bool, p *listProvider) (*Pages, *serviceListDB) {
	t.Helper()
	d := &serviceListDB{n: n, mixed: mixed}
	db := sql.OpenDB(d)
	t.Cleanup(func() { db.Close() })
	reg := server.NewRegistry()
	reg.Register(p)
	return &Pages{Svc: service.NewServicesRepo(db), Console: service.NewConsole(db, repo.NewServers(db), repo.NewProducts(db), reg, nil)}, d
}
func serviceListRequest(h *Pages, ctx context.Context, uid int64) *httptest.ResponseRecorder {
	ctx = middleware.WithSession(ctx, &middleware.Session{UserID: uid})
	w := httptest.NewRecorder()
	h.myServices(w, httptest.NewRequest(http.MethodGet, "/services", nil).WithContext(ctx))
	return w
}
func awaitList(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(4 * time.Second):
		t.Fatal("列表未及时结束")
		return nil
	}
}

func TestMyServicesConcurrency(t *testing.T) {
	p := &listProvider{fetch: func(ctx context.Context, _ int64) (server.HostDetail, error) {
		select {
		case <-time.After(30 * time.Millisecond):
			return server.HostDetail{IP: "198.51.100.1"}, nil
		case <-ctx.Done():
			return server.HostDetail{}, ctx.Err()
		}
	}}
	h, _ := serviceListFixture(t, 24, false, p)
	var counts [3]listConcurrency
	done := make(chan *httptest.ResponseRecorder, len(counts))
	for i := range counts {
		ctx := context.WithValue(context.Background(), listRequestKey{}, &counts[i])
		go func() { done <- serviceListRequest(h, ctx, 7) }()
	}
	for range counts {
		awaitList(t, done)
	}
	if p.peak.Load() > 8 {
		t.Errorf("跨请求列表并发=%d，不能超过8", p.peak.Load())
	}
	for i := range counts {
		if counts[i].peak.Load() > 4 {
			t.Errorf("请求%d并发=%d，不能超过4", i, counts[i].peak.Load())
		}
	}
	if p.calls.Load() != 72 || p.active.Load() != 0 {
		t.Fatalf("未尽力完成实时补全或响应后仍有任务：次数=%d，活跃=%d", p.calls.Load(), p.active.Load())
	}
	t.Logf("列表总并发峰值=%d，单请求峰值=%d/%d/%d，完成72次实时补全", p.peak.Load(), counts[0].peak.Load(), counts[1].peak.Load(), counts[2].peak.Load())
}

func TestMyServicesDeadline(t *testing.T) {
	var mu sync.Mutex
	var deadlines []time.Time
	var calls atomic.Int32
	p := &listProvider{fetch: func(ctx context.Context, _ int64) (server.HostDetail, error) {
		deadline, _ := ctx.Deadline()
		mu.Lock()
		deadlines = append(deadlines, deadline)
		mu.Unlock()
		if calls.Add(1) <= 4 {
			time.Sleep(100 * time.Millisecond)
			return server.HostDetail{}, nil
		}
		<-ctx.Done()
		return server.HostDetail{}, ctx.Err()
	}}
	h, _ := serviceListFixture(t, 40, false, p)
	start := time.Now()
	w := serviceListRequest(h, context.Background(), 7)
	var got struct{ List []struct{ IP string } }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.List) != 40 {
		t.Fatalf("超时列表响应异常：%s", w.Body.String())
	}
	fallbacks := 0
	for _, row := range got.List {
		if strings.HasPrefix(row.IP, "192.0.2.") {
			fallbacks++
		}
	}
	if fallbacks != 36 {
		t.Errorf("超时及未派发服务应保留快照：%d", fallbacks)
	}
	elapsed := time.Since(start)
	if elapsed < 2400*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("整个补全阶段耗时=%v，预期约2.5秒", elapsed)
	}
	if p.calls.Load() != 8 {
		t.Errorf("超时应停止派发，仅两批8次，实得%d", p.calls.Load())
	}
	for _, deadline := range deadlines {
		if !deadline.Equal(deadlines[0]) {
			t.Fatal("不同任务未共享整个阶段的截止时间")
		}
	}
	if p.active.Load() != 0 {
		t.Fatal("响应前未等待全部任务退出")
	}
}

func TestMyServicesCancellation(t *testing.T) {
	started := make(chan struct{}, 100)
	p := &listProvider{fetch: func(ctx context.Context, _ int64) (server.HostDetail, error) {
		started <- struct{}{}
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond) // 模拟上游取消后的收尾，handler 必须等待。
		return server.HostDetail{}, ctx.Err()
	}}
	h, _ := serviceListFixture(t, 100, false, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serviceListRequest(h, ctx, 7) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("上游未开始")
		}
	}
	cancel()
	awaitList(t, done)
	if p.calls.Load() != 4 || p.active.Load() != 0 {
		t.Fatalf("取消后继续派发或未等待任务：次数=%d，活跃=%d", p.calls.Load(), p.active.Load())
	}
}

func TestMyServicesQuotaCancellation(t *testing.T) {
	started := make(chan struct{}, 20)
	p := &listProvider{fetch: func(ctx context.Context, _ int64) (server.HostDetail, error) {
		if ctx.Value(listRequestKey{}) == nil {
			return server.HostDetail{IP: "198.51.100.9"}, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return server.HostDetail{}, ctx.Err()
	}}
	h, _ := serviceListFixture(t, 4, false, p)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), listRequestKey{}, &listConcurrency{}))
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { done <- serviceListRequest(h, ctx, 7) }()
	}
	for range 8 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("共享额度未占满")
		}
	}
	// 列表占满额度，不得阻断详情页等非列表请求。
	direct, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	if _, err := h.Console.HostDetail(direct, 7, 1); err != nil {
		t.Fatalf("列表额度影响非列表操作：%v", err)
	}
	waiting, stopWaiting := context.WithTimeout(ctx, 80*time.Millisecond)
	defer stopWaiting()
	waitDone := make(chan *httptest.ResponseRecorder, 1)
	start := time.Now()
	go func() { waitDone <- serviceListRequest(h, waiting, 7) }()
	awaitList(t, waitDone)
	if time.Since(start) > 500*time.Millisecond {
		t.Error("额度等待未响应请求取消")
	}
	if p.calls.Load() != 9 {
		t.Errorf("等待额度取消仍调用上游：%d", p.calls.Load())
	}
	cancel()
	for range 2 {
		awaitList(t, done)
	}
	if p.active.Load() != 0 {
		t.Fatal("取消后仍有活跃调用")
	}
}

func TestMyServicesEmptyAndAlreadyCancelled(t *testing.T) {
	for _, n := range []int{0, 20} {
		t.Run(fmt.Sprintf("服务数%d", n), func(t *testing.T) {
			p := &listProvider{fetch: func(context.Context, int64) (server.HostDetail, error) {
				return server.HostDetail{}, errors.New("不应访问上游")
			}}
			h, d := serviceListFixture(t, n, false, p)
			ctx, cancel := context.WithCancel(context.Background())
			if n > 0 {
				cancel()
			}
			defer cancel()
			serviceListRequest(h, ctx, 7)
			if p.calls.Load() != 0 || d.lookups.Load() != 0 {
				t.Fatal("空列表或已取消请求仍访问逐条查询/上游")
			}
		})
	}
}

func TestMyServicesSnapshotAndOwnership(t *testing.T) {
	p := &listProvider{fetch: func(_ context.Context, id int64) (server.HostDetail, error) {
		if id == 1 {
			return server.HostDetail{}, errors.New("模拟上游不可用")
		}
		return server.HostDetail{IP: "198.51.100.4", OSName: "实时系统", OSVersion: "1", Password: "不得泄露的密码", PanelURL: "不得泄露的面板"}, nil
	}}
	h, d := serviceListFixture(t, 4, true, p)
	w := serviceListRequest(h, context.Background(), 7)
	var got struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || len(got.List) != 4 {
		t.Fatalf("列表响应异常：%d %s", w.Code, w.Body.String())
	}
	for i, row := range got.List {
		ip, os := fmt.Sprintf("192.0.2.%d", i+1), "Debian-12"
		if i == 3 {
			ip, os = "198.51.100.4", "实时系统-1"
		}
		if row["ip"] != ip || row["os"] != os {
			t.Errorf("实时或快照兜底错误：%+v", row)
		}
		if len(row) != 16 {
			t.Errorf("响应字段发生变化：%+v", row)
		}
	}
	if strings.Contains(w.Body.String(), "不得泄露") {
		t.Fatal("敏感详情进入列表响应")
	}
	if p.calls.Load() != 2 || d.lookups.Load() != 2 {
		t.Errorf("无绑定或未激活服务不应逐条查库：上游=%d，解析=%d", p.calls.Load(), d.lookups.Load())
	}
	foreign := serviceListRequest(h, context.Background(), 8)
	if err := json.Unmarshal(foreign.Body.Bytes(), &got); err != nil || len(got.List) != 0 {
		t.Fatal("列表泄露其他用户服务")
	}
	if _, err := h.Console.HostDetail(context.Background(), 8, 1); err == nil || p.calls.Load() != 2 {
		t.Fatal("实时详情未校验归属")
	}
}
