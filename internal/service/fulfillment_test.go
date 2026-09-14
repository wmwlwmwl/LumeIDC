package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// setupFulfillment 造一套「服务器 + 产品 + 服务 + 待重试任务」，测试结束自建自删。
func setupFulfillment(t *testing.T, d *sql.DB, retryEnabled bool, retryMinutes int) (jobID, serviceID int64) {
	t.Helper()
	ctx := context.Background()

	var serverID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,retry_later_enabled,retry_later_interval_minutes)
		 VALUES('单元测试-重试策略','fake','http://127.0.0.1:1',$1,$2) RETURNING id`,
		retryEnabled, retryMinutes).Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	var uid int64
	if err := d.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'测试') RETURNING id`, "fulfillment-policy-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var pid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id) VALUES(NULL,'单元测试-重试策略',-1,$1) RETURNING id`,
		serverID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,server_id,status) VALUES($1,$2,$3,0) RETURNING id`,
		uid, pid, serverID).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,kind,cycle,status,attempts,dedupe_key,lease_until)
		 VALUES($1,'provision','monthly','running',3,$2,now()+interval '3 minutes') RETURNING id`,
		serviceID, "svc-retry-test:"+time.Now().Format("150405.000000000")).Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, s := range []struct {
			q   string
			arg int64
		}{
			{`DELETE FROM services WHERE id=$1`, serviceID}, // 级联删任务
			{`DELETE FROM products WHERE id=$1`, pid},
			{`DELETE FROM servers WHERE id=$1`, serverID},
			{`DELETE FROM users WHERE id=$1`, uid},
		} {
			if _, err := d.ExecContext(ctx, s.q, s.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", s.q, err)
			}
		}
	})
	return jobID, serviceID
}

// jobState 读任务状态、已用重试次数、下次重试时间。
func jobState(t *testing.T, d *sql.DB, jobID int64) (string, int, time.Time) {
	t.Helper()
	var status string
	var attempts int
	var nextAt time.Time
	if err := d.QueryRow(
		`SELECT status,attempts,next_attempt_at FROM fulfillment_jobs WHERE id=$1`, jobID).
		Scan(&status, &attempts, &nextAt); err != nil {
		t.Fatal(err)
	}
	return status, attempts, nextAt
}

// 上游重试策略：关闭后"等外部条件"的失败应立即转人工，而不是继续自动重试。
func TestMarkRetryLaterFollowsServerPolicy(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	jobs := repo.NewFulfillmentJobs(d)
	servers := repo.NewServers(d)
	ff := &Fulfillment{Jobs: jobs, Lifecycle: &Lifecycle{Servers: servers}}
	cause := &server.RetryLaterError{Msg: "账户余额不足"}

	t.Run("开启：保持重试且不消耗重试次数", func(t *testing.T) {
		jobID, serviceID := setupFulfillment(t, d, true, 30)
		if err := ff.markRetryLater(ctx, &repo.FulfillmentJob{ID: jobID, ServiceID: serviceID}, cause); err != nil {
			t.Fatalf("应按策略保持重试: %v", err)
		}
		status, attempts, nextAt := jobState(t, d, jobID)
		if status != "retry" {
			t.Fatalf("状态应为 retry，实得 %q", status)
		}
		if attempts != 0 {
			t.Fatalf("attempts 应归零（否则累计到 8 次仍被判 dead），实得 %d", attempts)
		}
		if nextAt.Before(time.Now().Add(29 * time.Minute)) {
			t.Fatalf("间隔应取服务器配置的 30 分钟，实得 %v", nextAt)
		}
	})

	t.Run("关闭：立即转人工复核", func(t *testing.T) {
		jobID, serviceID := setupFulfillment(t, d, false, 10)
		if err := ff.markRetryLater(ctx, &repo.FulfillmentJob{ID: jobID, ServiceID: serviceID}, cause); err != nil {
			t.Fatalf("应转人工复核: %v", err)
		}
		status, _, _ := jobState(t, d, jobID)
		if status != "manual_review" {
			t.Fatalf("关闭自动重试后应转 manual_review，实得 %q", status)
		}
	})
}

type interruptedRenewProvider struct{ server.Provider }

func (interruptedRenewProvider) Code() string { return "fake-upgrade" }
func (interruptedRenewProvider) Renew(context.Context, server.Config, int64, string, server.CheckpointStore) error {
	return context.DeadlineExceeded
}

func TestFulfillmentInterruptedOperationIsQuarantined(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRenewFixture(t, d)
	if _, err := d.Exec(`UPDATE services SET upstream_provider='fake-upgrade' WHERE id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET status='queued' WHERE service_id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	registry := server.NewRegistry()
	registry.Register(interruptedRenewProvider{})
	jobs := repo.NewFulfillmentJobs(d)
	ff := &Fulfillment{Jobs: jobs, Lifecycle: &Lifecycle{db: d, Providers: registry, Servers: repo.NewServers(d), Provisions: repo.NewProvisionRepo(d)}}
	if worked, err := ff.ProcessOne(ctx); err != nil || !worked {
		t.Fatalf("中断任务应被安全隔离：%v，%v", worked, err)
	}
	var state string
	var recovery bool
	if err := d.QueryRow(`SELECT status,recovery_required FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&state, &recovery); err != nil || state != "manual_review" || !recovery {
		t.Fatalf("超时不能自动重放：%s，%v，%v", state, recovery, err)
	}
}

func TestRecoveredFulfillmentBlocksAdminWrites(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRenewFixture(t, d)
	jobs := repo.NewFulfillmentJobs(d)
	provisions := repo.NewProvisionRepo(d)
	pay := &Payment{db: d, Jobs: jobs, Provisions: provisions}
	if err := provisions.SetCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice, "保留原账单"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE fulfillment_jobs SET status='running',lease_until=now()-interval '1 second' WHERE service_id=$1`, fx.svcID); err != nil {
		t.Fatal(err)
	}
	if job, err := jobs.Claim(ctx, time.Minute); err != nil || job != nil {
		t.Fatalf("恢复只应隔离：%+v，%v", job, err)
	}
	for name, action := range map[string]func() error{
		"重试续费":  func() error { return pay.RetryRenew(ctx, fx.svcID) },
		"取消续费":  func() error { return pay.RefundRenew(ctx, fx.userID, fx.svcID, "未知结果") },
		"取消升级":  func() error { return pay.RefundUpgrade(ctx, fx.userID, fx.svcID, "未知结果") },
		"取消开通":  func() error { return pay.RefundPendingService(ctx, fx.userID, fx.svcID, "未知结果") },
		"覆写检查点": func() error { return provisions.SetCheckpoint(ctx, fx.svcID, renewDoneCkKey(fx.orderID), "refunded") },
		"删除检查点": func() error { return provisions.DeleteCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice) },
	} {
		if err := action(); err == nil {
			t.Fatalf("%s不能绕过未知结果隔离", name)
		}
	}
	if v, ok, err := provisions.GetCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice); err != nil || !ok || v != "保留原账单" {
		t.Fatalf("不能丢失账单证据：%q，%v，%v", v, ok, err)
	}
	if _, ok, err := provisions.GetCheckpoint(ctx, fx.svcID, renewPriceOkKey(fx.orderID)); err != nil || ok {
		t.Fatalf("拒绝重试不能写入价格确认：%v，%v", ok, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("不能添加额外任务：%d，%v", count, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM refunds WHERE order_id=$1`, fx.orderID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("隔离任务不能自动进入退款处置：%d，%v", count, err)
	}
}

// fulfillmentMemoryDB 只模拟本文件需要的 SQL 返回值和连接占用，不模拟 PostgreSQL 锁/事务语义。
// 测试通过钩子精确暂停领取、校验和执行，始终不访问真实数据库。
type fulfillmentMemoryDB struct {
	mu      sync.Mutex
	queued  int
	claimed int64
	results map[int64]string
	hook    func(context.Context, string, bool) error
}

type fulfillmentMemoryConn struct {
	db       *fulfillmentMemoryDB
	selected int64
	locked   bool
}
type fulfillmentMemoryTx struct{}
type fulfillmentMemoryRows struct {
	values  [][]driver.Value
	columns int
}

func (d *fulfillmentMemoryDB) Open(string) (driver.Conn, error) {
	return &fulfillmentMemoryConn{db: d}, nil
}
func (d *fulfillmentMemoryDB) Connect(context.Context) (driver.Conn, error) { return d.Open("") }
func (d *fulfillmentMemoryDB) Driver() driver.Driver                        { return d }
func (c *fulfillmentMemoryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("测试不支持预编译")
}
func (c *fulfillmentMemoryConn) Close() error              { return nil }
func (c *fulfillmentMemoryConn) Begin() (driver.Tx, error) { return fulfillmentMemoryTx{}, nil }
func (fulfillmentMemoryTx) Commit() error                  { return nil }
func (fulfillmentMemoryTx) Rollback() error                { return nil }
func (r *fulfillmentMemoryRows) Columns() []string         { return make([]string, r.columns) }
func (r *fulfillmentMemoryRows) Close() error              { return nil }
func (r *fulfillmentMemoryRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
func fulfillmentRow(values ...driver.Value) driver.Rows {
	return &fulfillmentMemoryRows{values: [][]driver.Value{values}, columns: len(values)}
}
func (c *fulfillmentMemoryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.db.hook != nil {
		if err := c.db.hook(ctx, query, c.locked); err != nil {
			return nil, err
		}
	}
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	switch {
	case strings.Contains(query, "pg_try_advisory_lock"):
		c.locked = true
		return fulfillmentRow(true), nil
	case strings.Contains(query, "SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id="):
		return fulfillmentRow(true), nil
	case strings.Contains(query, "SELECT s.id FROM services s"):
		if !strings.Contains(query, "j.status IN ('queued','retry')") || c.db.queued == 0 {
			return &fulfillmentMemoryRows{columns: 1}, nil
		}
		c.db.queued--
		c.db.claimed++
		c.selected = c.db.claimed
		return fulfillmentRow(c.selected), nil
	case strings.Contains(query, "SELECT id,service_id,order_id,kind,cycle,attempts"):
		return fulfillmentRow(c.selected, c.selected, c.selected, "provision", "monthly", int64(0)), nil
	case strings.Contains(query, "RETURNING claim_version"):
		return fulfillmentRow(int64(1)), nil
	case strings.Contains(query, "SELECT server_id,coalesce(upstream_provider"):
		return fulfillmentRow(nil, "", int64(0)), nil
	case strings.Contains(query, "SELECT id FROM services"), strings.Contains(query, "SELECT id FROM fulfillment_jobs"):
		return fulfillmentRow(args[0].Value), nil
	default:
		return nil, fmt.Errorf("测试未模拟查询：%s", query)
	}
}
func (c *fulfillmentMemoryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.db.hook != nil {
		if err := c.db.hook(ctx, query, c.locked); err != nil {
			return nil, err
		}
	}
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	switch {
	case strings.Contains(query, "pg_advisory_unlock"):
		c.locked = false
	case strings.Contains(query, "UPDATE fulfillment_jobs SET status="):
		state := "retry"
		if strings.Contains(query, "status='succeeded'") {
			state = "succeeded"
		}
		if strings.Contains(query, "status='manual_review'") {
			state = "manual_review"
		}
		c.db.results[args[0].Value.(int64)] = state
	case strings.Contains(query, "UPDATE services SET"):
	default:
		return nil, fmt.Errorf("测试未模拟写入：%s", query)
	}
	return driver.RowsAffected(1), nil
}
func newFulfillmentMemory(t *testing.T, queued int) (*Fulfillment, *fulfillmentMemoryDB, *sql.DB) {
	t.Helper()
	memory := &fulfillmentMemoryDB{queued: queued, results: make(map[int64]string)}
	db := sql.OpenDB(memory)
	db.SetMaxOpenConns(25)
	t.Cleanup(func() { db.Close() })
	return &Fulfillment{Jobs: repo.NewFulfillmentJobs(db), Payment: &Payment{db: db}}, memory, db
}

func TestFulfillmentAdmissionBeforeClaim(t *testing.T) {
	f, memory, _ := newFulfillmentMemory(t, 5)
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	memory.hook = func(ctx context.Context, query string, _ bool) error {
		if strings.Contains(query, "SELECT s.id FROM services s") && !strings.Contains(query, "j.status IN ('queued','retry')") {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 4)
	for range 4 {
		go func() { _, err := f.ProcessOne(ctx); done <- err }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("未进入四个履约执行槽位")
		}
	}
	short, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	worked, err := f.ProcessOne(short)
	stop()
	if worked || err != nil {
		t.Errorf("同步入口满额必须不领取且立即返回，实得 %v，%v", worked, err)
	}
	select {
	case <-entered:
		t.Error("第五个调用不应进入领取/借连接流程")
	default:
	}
	once.Do(func() { close(release) })
	for range 4 {
		if err := <-done; err != nil {
			t.Errorf("已接纳任务失败：%v", err)
		}
	}
	f.Drain(context.Background(), 10)
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.queued != 0 || len(memory.results) != 5 {
		t.Fatalf("额度释放后轮询必须处理所有持久任务：剩余%d，结果%d", memory.queued, len(memory.results))
	}
	for _, state := range memory.results {
		if state != "succeeded" {
			t.Errorf("本地开通应成功，实得%s", state)
		}
	}
}

func TestFulfillmentTriggerSharesAdmission(t *testing.T) {
	f, memory, _ := newFulfillmentMemory(t, 6)
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	memory.hook = func(ctx context.Context, query string, _ bool) error {
		if strings.Contains(query, "SELECT s.id FROM services s") && !strings.Contains(query, "j.status IN ('queued','retry')") {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	for range 4 {
		f.TriggerDrain(context.Background(), 1)
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("异步入口未启动")
		}
	}
	// 不同 Fulfillment 对象、同步轮询和 HTTP 触发必须共享单进程额度。
	other := &Fulfillment{Jobs: f.Jobs, Payment: f.Payment}
	for range 1000 {
		other.TriggerDrain(context.Background(), 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	other.Drain(ctx, 1)
	if ctx.Err() != nil {
		t.Error("满额轮询不应排队等待")
	}
	if worked, err := other.ProcessOne(ctx); worked || err != nil {
		t.Errorf("同步入口未共享额度：%v，%v", worked, err)
	}
	if len(fulfillmentSlots) != 4 {
		t.Errorf("异步触发必须在创建协程前占用四个额度，实得%d", len(fulfillmentSlots))
	}
	select {
	case <-entered:
		t.Error("满额触发仍进入领取")
	default:
	}
	once.Do(func() { close(release) })
	deadline := time.After(time.Second)
	for len(fulfillmentSlots) != 0 {
		select {
		case <-deadline:
			t.Fatal("异步退出未释放额度")
		case <-time.After(time.Millisecond):
		}
	}
	other.Drain(context.Background(), 10)
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.queued != 0 || len(memory.results) != 6 {
		t.Fatalf("丢失唤醒不能丢持久任务：剩余%d，结果%d", memory.queued, len(memory.results))
	}
}

func TestFulfillmentPoolWaitCancellationReleasesAdmission(t *testing.T) {
	f, _, db := newFulfillmentMemory(t, 1)
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if worked, err := f.ProcessOne(ctx); worked || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("借池等待必须可取消且不能领取任务：%v，%v", worked, err)
	}
	if len(fulfillmentSlots) != 0 {
		t.Fatal("借池取消后额度未释放")
	}
	conn.Close()
	db.SetMaxOpenConns(2)
	if worked, err := f.ProcessOne(context.Background()); !worked || err != nil {
		t.Fatalf("连接恢复后原任务必须仍可处理：%v，%v", worked, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if worked, err := f.ProcessOne(canceled); worked || !errors.Is(err, context.Canceled) {
		t.Fatalf("取消上下文应立即返回：%v，%v", worked, err)
	}
}

func TestFulfillmentFailureReleasesAdmission(t *testing.T) {
	for _, stage := range []string{"SELECT s.id FROM services s", "pg_try_advisory_lock", "SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id=", "status='succeeded'"} {
		t.Run(stage, func(t *testing.T) {
			f, memory, _ := newFulfillmentMemory(t, 1)
			failure := errors.New("受控数据库失败")
			memory.hook = func(_ context.Context, query string, _ bool) error {
				if strings.Contains(query, stage) {
					return failure
				}
				return nil
			}
			if _, err := f.ProcessOne(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("不能吞掉错误：%v", err)
			}
			if len(fulfillmentSlots) != 0 {
				t.Fatal("错误退出未释放执行额度")
			}
		})
	}
}

func TestFulfillmentBudgetStartsBeforeClaim(t *testing.T) {
	f, memory, _ := newFulfillmentMemory(t, 1)
	var workDeadline time.Time
	stages := make(map[string]bool)
	memory.hook = func(ctx context.Context, query string, _ bool) error {
		for _, stage := range []string{"SELECT s.id FROM services s", "pg_try_advisory_lock", "SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id=", "SELECT server_id,coalesce(upstream_provider"} {
			if !strings.Contains(query, stage) {
				continue
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 2*time.Minute {
				return errors.New("领取及借连接前缺少整体工作预算")
			}
			if workDeadline.IsZero() {
				workDeadline = deadline
			} else if !deadline.Equal(workDeadline) {
				return errors.New("领取、锁连接、校验及执行必须共享同一截止时间")
			}
			stages[stage] = true
		}
		return nil
	}
	if worked, err := f.ProcessOne(context.Background()); !worked || err != nil {
		t.Fatalf("任务应在共享预算内完成：%v，%v", worked, err)
	}
	if len(stages) != 4 {
		t.Fatalf("预算检查应覆盖领取、锁连接、校验和执行，实得%d个阶段", len(stages))
	}
}

func TestFulfillmentValidationReusesLockConnection(t *testing.T) {
	t.Run("单连接池校验不再借池", func(t *testing.T) {
		f, _, db := newFulfillmentMemory(t, 0)
		db.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, unlock, err := f.Jobs.TryExecutionLock(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		// 真实 database/sql 池已全部占用；旧实现在此会二次借池直到超时。
		if err := f.Jobs.ValidateClaim(ctx, conn, &repo.FulfillmentJob{ID: 1, ServiceID: 1, ClaimVersion: 1}); err != nil {
			t.Fatalf("单连接池应能在持锁连接上完成校验：%v", err)
		}
		if stats := db.Stats(); stats.InUse != 1 || stats.WaitCount != 0 {
			t.Fatalf("校验不应再次借池：占用%d，等待次数%d", stats.InUse, stats.WaitCount)
		}
		oldCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
		defer stop()
		var valid bool
		err = db.QueryRowContext(oldCtx, `SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id=$1)`, 1).Scan(&valid)
		if !errors.Is(err, context.DeadlineExceeded) || db.Stats().WaitCount != 1 {
			t.Fatalf("对照：旧式池查询应因持锁耗尽连接而超时，实得%v", err)
		}
	})
	f, memory, _ := newFulfillmentMemory(t, 1)
	memory.hook = func(_ context.Context, query string, locked bool) error {
		if strings.Contains(query, "SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE id=") && !locked {
			return errors.New("校验借用了第二条连接，会耗尽连接池")
		}
		return nil
	}
	if worked, err := f.ProcessOne(context.Background()); !worked || err != nil {
		t.Fatalf("必须复用锁连接校验并完成履约：%v，%v", worked, err)
	}
}

func TestFulfillmentCancellationAndResults(t *testing.T) {
	for _, mode := range []string{"成功", "业务错误", "人工复核", "等待重试", "执行超时", "执行取消"} {
		t.Run(mode, func(t *testing.T) {
			f, memory, _ := newFulfillmentMemory(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var businessErr error = errors.New("可重试业务失败")
			if mode == "人工复核" {
				businessErr = &server.ManualReviewError{Msg: "需人工确认"}
			}
			if mode == "等待重试" {
				businessErr = &server.RetryLaterError{Msg: "上游余额不足"}
			}
			memory.hook = func(ctx context.Context, query string, _ bool) error {
				if strings.Contains(query, "UPDATE fulfillment_jobs SET status=") && !strings.Contains(query, "RETURNING claim_version") {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second || ctx.Err() != nil {
						return errors.New("结果写回必须有独立的有效短预算")
					}
				}
				if strings.Contains(query, "SELECT server_id,coalesce(upstream_provider") {
					if mode == "业务错误" || mode == "人工复核" || mode == "等待重试" {
						return businessErr
					}
					if mode == "执行超时" {
						return context.DeadlineExceeded
					}
					if mode == "执行取消" {
						cancel()
						return ctx.Err()
					}
				}
				return nil
			}
			worked, err := f.ProcessOne(ctx)
			if !worked {
				t.Fatal("应已领取任务")
			}
			want := "succeeded"
			if mode == "业务错误" || mode == "人工复核" || mode == "等待重试" {
				want = "retry"
				if mode == "人工复核" {
					want = "manual_review"
				}
				if !errors.Is(err, businessErr) {
					t.Fatalf("应保留原业务错误：%v", err)
				}
			} else if err != nil {
				t.Errorf("结果落库应成功：%v", err)
			}
			if mode == "执行超时" || mode == "执行取消" {
				want = "manual_review"
			}
			memory.mu.Lock()
			state := memory.results[1]
			memory.mu.Unlock()
			if state != want {
				t.Errorf("取消亦必须保留结果写回预算：应为%s，实得%s", want, state)
			}
			if worked, err := f.ProcessOne(context.Background()); worked || err != nil {
				t.Fatalf("退出后额度应释放：%v，%v", worked, err)
			}
		})
	}
}

// 查不到服务器（本地服务/服务不存在）时用默认策略，而不是报错——否则失败处理本身会挂。
func TestRetryLaterPolicyDefaults(t *testing.T) {
	d := testDB(t)
	enabled, minutes, err := repo.NewServers(d).RetryLaterPolicy(context.Background(), -1)
	if err != nil {
		t.Fatalf("查不到应返回默认值而不是错误: %v", err)
	}
	if !enabled || minutes != repo.DefaultRetryLaterMinutes {
		t.Fatalf("默认应为 启用/%d 分钟，实得 %v/%d", repo.DefaultRetryLaterMinutes, enabled, minutes)
	}
}
