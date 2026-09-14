package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// 对账恢复（重引擎）测试：覆盖 Payment.FulfillmentRecovery / ConfirmFulfillmentRecovery。
// 隔离库直连（TEST_DATABASE_DSN），无 DSN 时跳过（模式同 payment_test.go）。

// recoveryEngineFx 夹具句柄。
type recoveryEngineFx struct {
	db                                        *sql.DB
	jobID, svcID, userID, productID, serverID int64
	orderID, invoiceID                        int64
}

// recoveryEvidence 合法核对证据（≥10 字）。
const recoveryEvidence = "已在上游核实实例与账单一致，无待付账单"

// setupRecoveryEngine 夹具：按 kind 造一条"上游结果未知"隔离任务（claim_version=1）。
// 服务/订单/账单/周期授权按恢复校验要求造好：订单已支付、账单唯一且已付、无退款、无升级失败余额流水。
// 059 触发器禁止隔离期间改服务关键字段，因此先建服务并改成目标状态，最后才插入隔离任务。
// renew 场景预置上游账单与价格确认检查点（CanResume=false，走"已生效"恢复路径）。
func setupRecoveryEngine(t *testing.T, d *sql.DB, kind string) recoveryEngineFx {
	t.Helper()
	ctx := context.Background()
	uniq := time.Now().Format("150405.000000000")
	fx := recoveryEngineFx{db: d}

	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url) VALUES('单元测试-对账恢复重引擎','zjmf','http://127.0.0.1:1') RETURNING id`).
		Scan(&fx.serverID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"rcveng-"+uniq+"@example.invalid").Scan(&fx.userID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock,server_id) VALUES(NULL,'单元测试-对账恢复重引擎',-1,$1) RETURNING id`,
		fx.serverID).Scan(&fx.productID); err != nil {
		t.Fatal(err)
	}
	psID, err := repo.NewProducts(d).DefaultPricesetID(ctx)
	if err != nil {
		t.Skip("库中暂无价格组，跳过")
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,server_id,status,upstream_provider) VALUES($1,$2,$3,0,'zjmf') RETURNING id`,
		fx.userID, fx.productID, fx.serverID).Scan(&fx.svcID); err != nil {
		t.Fatal(err)
	}

	jobKind := "provision"
	if kind == "renew" {
		jobKind = "renew"
		// 续费场景：服务已激活、已绑定上游、"续费待处理"，到期时间固定。
		if _, err := d.ExecContext(ctx,
			`UPDATE services SET status=1,upstream_host_id=555,transition_state='renew_pending',cycle='monthly',
			  expires_at='2027-06-15 12:00:00+00',provision_error='续费失败：需要人工处理' WHERE id=$1`, fx.svcID); err != nil {
			t.Fatal(err)
		}
		// 已支付的续费订单 + 已付账单 + 周期授权（恢复前置条件：本地到期时间已延长）。
		if err := d.QueryRowContext(ctx,
			`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,service_id,kind,config_snapshot)
			 VALUES($1,$2,$3,'monthly','11.00',1,$4,'renew','{}') RETURNING id`,
			fx.userID, fx.productID, psID, fx.svcID).Scan(&fx.orderID); err != nil {
			t.Fatal(err)
		}
	} else {
		// 开通场景：新购订单未关联服务（service_id 为空），服务挂在该订单上等待开通。
		if err := d.QueryRowContext(ctx,
			`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,kind,config_snapshot)
			 VALUES($1,$2,$3,'monthly','11.00',1,'provision','{}') RETURNING id`,
			fx.userID, fx.productID, psID).Scan(&fx.orderID); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,status,gateway,paid_at)
		 VALUES($1,$2,$3,'11.00',1,'balance',now()) RETURNING id`,
		"RCVENG-"+uniq, fx.userID, fx.orderID).Scan(&fx.invoiceID); err != nil {
		t.Fatal(err)
	}
	if jobKind == "renew" {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO service_period_grants(service_id,invoice_id,cycle) VALUES($1,$2,'monthly')`,
			fx.svcID, fx.invoiceID); err != nil {
			t.Fatal(err)
		}
		// 隔离前留下的中间检查点：上游账单号与价格确认标记（"已生效"恢复后应被清除）。
		if _, err := d.ExecContext(ctx,
			`UPDATE services SET provision_data=jsonb_build_object($2::text,'888',$3::text,'1') WHERE id=$1`,
			fx.svcID, server.CheckpointRenewInvoice, renewPriceOkKey(fx.orderID)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.ExecContext(ctx,
			`UPDATE services SET order_id=$2 WHERE id=$1`, fx.svcID, fx.orderID); err != nil {
			t.Fatal(err)
		}
	} else if _, err := d.ExecContext(ctx,
		`UPDATE services SET order_id=$2,provision_error=$3 WHERE id=$1`,
		fx.svcID, fx.orderID, repo.ErrFulfillmentRecoveryRequired.Error()); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,status,claim_version,recovery_required,attempts,dedupe_key,last_error)
		 VALUES($1,$2,$3,'monthly','manual_review',1,true,3,$4,$5) RETURNING id`,
		fx.svcID, fx.orderID, jobKind, "rcveng:"+jobKind+":"+uniq,
		repo.ErrFulfillmentRecoveryRequired.Error()).Scan(&fx.jobID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, s := range []struct {
			q   string
			arg int64
		}{
			{`DELETE FROM service_period_grants WHERE service_id=$1`, fx.svcID},
			{`DELETE FROM invoices WHERE order_id=$1`, fx.orderID},
			{`DELETE FROM orders WHERE id=$1`, fx.orderID},
			{`DELETE FROM admin_logs WHERE target_type='service' AND target_id=$1`, fx.svcID},
			{`DELETE FROM services WHERE id=$1`, fx.svcID},
			{`DELETE FROM products WHERE id=$1`, fx.productID},
			{`DELETE FROM servers WHERE id=$1`, fx.serverID},
			{`DELETE FROM users WHERE id=$1`, fx.userID},
		} {
			if _, err := d.ExecContext(ctx, s.q, s.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", s.q, err)
			}
		}
	})
	return fx
}

// recoveryPayment 造重引擎 Payment；triggered 计数器记录 triggerFulfillment 次数。
func recoveryPayment(d *sql.DB, triggered *int) *Payment {
	return &Payment{db: d, Servers: repo.NewServers(d), TriggerFulfillment: func() { *triggered++ }}
}

// recoveryConfirm 构造合法确认请求的便捷封装。
func recoveryConfirm(fx recoveryEngineFx, decision string, verifiedHostID int64, mutate func(*RecoveryConfirmation)) RecoveryConfirmation {
	req := RecoveryConfirmation{
		JobID: fx.jobID, ExpectedVersion: 1, Decision: decision,
		Evidence: recoveryEvidence, VerifiedHostID: verifiedHostID,
		RemoteStable: true, BillingVerified: true, DeliveryVerified: true,
	}
	if mutate != nil {
		mutate(&req)
	}
	return req
}

// assertRecoveryIsolated 断言该服务的隔离任务原样保留（被拒绝后不得解锁）。
func assertRecoveryIsolated(t *testing.T, d *sql.DB, svcID int64) {
	t.Helper()
	var status string
	var recovery bool
	if err := d.QueryRow(
		`SELECT status,recovery_required FROM fulfillment_jobs WHERE service_id=$1 ORDER BY id LIMIT 1`, svcID).
		Scan(&status, &recovery); err != nil {
		t.Fatal(err)
	}
	if status != "manual_review" || !recovery {
		t.Fatalf("隔离必须保持：%s recovery=%v", status, recovery)
	}
}

func TestConfirmRecoveryProvisionCompleted(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRecoveryEngine(t, d, "provision")
	var triggered int
	p := recoveryPayment(d, &triggered)

	// 摘要可读、无阻断、可判定。
	sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
	if err != nil {
		t.Fatalf("读取恢复摘要失败: %v", err)
	}
	if sum.Kind != "provision" || sum.OrderID != fx.orderID || sum.JobID != fx.jobID ||
		sum.Amount != "11.00" || sum.InvoiceStatus != 1 || sum.PendingJobs != 1 ||
		sum.BlockReason != "" || !sum.CanResume {
		t.Fatalf("摘要异常：%+v", sum)
	}

	if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
		recoveryConfirm(fx, "confirmed_completed", 777, nil)); err != nil {
		t.Fatalf("provision 已生效恢复应成功: %v", err)
	}
	var status string
	var recovery bool
	if err := d.QueryRow(`SELECT status,recovery_required FROM fulfillment_jobs WHERE id=$1`, fx.jobID).
		Scan(&status, &recovery); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || recovery {
		t.Fatalf("任务应终结并解除隔离：%s recovery=%v", status, recovery)
	}
	var st int16
	var host sql.NullInt64
	if err := d.QueryRow(`SELECT status,upstream_host_id FROM services WHERE id=$1`, fx.svcID).
		Scan(&st, &host); err != nil {
		t.Fatal(err)
	}
	if st != 1 || !host.Valid || host.Int64 != 777 {
		t.Fatalf("应补记绑定并激活：status=%d host=%v", st, host)
	}
	// 上游检查点必须带 host id（后续重跑据此短路，不再重复开通）。
	var ck string
	if err := d.QueryRow(`SELECT coalesce(provision_data->>$2,'') FROM services WHERE id=$1`,
		fx.svcID, server.CheckpointUpstreamHostIDs).Scan(&ck); err != nil || ck != "777" {
		t.Fatalf("检查点应有上游 host id：%q，%v", ck, err)
	}
	var logs int
	if err := d.QueryRow(`SELECT count(*) FROM admin_logs WHERE action='fulfillment_recovery' AND target_id=$1`, fx.svcID).
		Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("应写一条后台审计：%d，%v", logs, err)
	}
	if triggered != 0 {
		t.Fatalf("已生效恢复不应触发队列：%d", triggered)
	}
}

func TestConfirmRecoveryProvisionHostConflict(t *testing.T) {
	d := testDB(t)
	fx := setupRecoveryEngine(t, d, "provision")
	p := recoveryPayment(d, nil)
	// 同一上游账户下另一服务已绑定同一 host：禁止把一个 host 分配给两个服务。
	second := int64(0)
	if err := d.QueryRow(
		`INSERT INTO services(user_id,product_id,server_id,status,upstream_host_id,upstream_provider)
		 VALUES($1,$2,$3,1,777,'zjmf') RETURNING id`, fx.userID, fx.productID, fx.serverID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.Exec(`DELETE FROM services WHERE id=$1`, second); err != nil {
			t.Errorf("清理冲突服务失败: %v", err)
		}
	})
	if err := p.ConfirmFulfillmentRecovery(context.Background(), 9, fx.svcID, "203.0.113.9:1234",
		recoveryConfirm(fx, "confirmed_completed", 777, nil)); err == nil {
		t.Fatal("跨服务主机冲突应拒绝")
	}
	assertRecoveryIsolated(t, d, fx.svcID)
}

func TestConfirmRecoveryRenewCompleted(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRecoveryEngine(t, d, "renew")
	p := recoveryPayment(d, nil)

	if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
		recoveryConfirm(fx, "confirmed_completed", 555, nil)); err != nil {
		t.Fatalf("renew 已生效恢复应成功: %v", err)
	}
	var tr, pe, expires string
	if err := d.QueryRow(
		`SELECT coalesce(transition_state,''),coalesce(provision_error,''),to_char(expires_at,'YYYY-MM-DD')
		 FROM services WHERE id=$1`, fx.svcID).Scan(&tr, &pe, &expires); err != nil {
		t.Fatal(err)
	}
	if tr != "" || pe != "" {
		t.Fatalf("应解除续费待处理：%q %q", tr, pe)
	}
	if expires != "2027-06-15" {
		t.Fatalf("恢复不能动到期时间，实得 %s", expires)
	}
	var grants int
	if err := d.QueryRow(`SELECT count(*) FROM service_period_grants WHERE service_id=$1`, fx.svcID).
		Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("周期授权不能变化：%d，%v", grants, err)
	}
	// 订单级成功检查点写入；服务级上游账单与价格确认键删除，避免下次续费误复用。
	var ck string
	if err := d.QueryRow(`SELECT coalesce(provision_data->>$2,'') FROM services WHERE id=$1`,
		fx.svcID, renewDoneCkKey(fx.orderID)).Scan(&ck); err != nil || ck == "" {
		t.Fatalf("应写订单级续费成功检查点：%q，%v", ck, err)
	}
	provisions := repo.NewProvisionRepo(d)
	if v, ok, _ := provisions.GetCheckpoint(ctx, fx.svcID, server.CheckpointRenewInvoice); ok {
		t.Fatalf("应删除服务级上游账单检查点，实得 %q", v)
	}
	if v, ok, _ := provisions.GetCheckpoint(ctx, fx.svcID, renewPriceOkKey(fx.orderID)); ok {
		t.Fatalf("应删除价格确认检查点，实得 %q", v)
	}
	var status string
	var recovery bool
	if err := d.QueryRow(`SELECT status,recovery_required FROM fulfillment_jobs WHERE id=$1`, fx.jobID).
		Scan(&status, &recovery); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || recovery {
		t.Fatalf("任务应终结并解除隔离：%s recovery=%v", status, recovery)
	}
	var logs int
	if err := d.QueryRow(`SELECT count(*) FROM admin_logs WHERE action='fulfillment_recovery' AND target_id=$1`, fx.svcID).
		Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("应写一条后台审计：%d，%v", logs, err)
	}
}

func TestConfirmRecoveryNotExecuted(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	t.Run("无上游账单检查点：任务回队列并触发履约", func(t *testing.T) {
		fx := setupRecoveryEngine(t, d, "provision")
		var triggered int
		p := recoveryPayment(d, &triggered)
		if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
			recoveryConfirm(fx, "confirmed_not_executed", 0, nil)); err != nil {
			t.Fatalf("未执行恢复应成功: %v", err)
		}
		var status string
		var recovery bool
		if err := d.QueryRow(`SELECT status,recovery_required FROM fulfillment_jobs WHERE id=$1`, fx.jobID).
			Scan(&status, &recovery); err != nil {
			t.Fatal(err)
		}
		if status != "queued" || recovery {
			t.Fatalf("任务应回队列并解除隔离：%s recovery=%v", status, recovery)
		}
		var next time.Time
		if err := d.QueryRow(`SELECT next_attempt_at FROM fulfillment_jobs WHERE id=$1`, fx.jobID).Scan(&next); err != nil {
			t.Fatal(err)
		}
		if next.After(time.Now().Add(2 * time.Second)) {
			t.Fatalf("应立即可重跑：next_attempt_at=%v", next)
		}
		if triggered != 1 {
			t.Fatalf("未执行恢复应触发一次履约队列：%d", triggered)
		}
		// 未执行路径不改服务绑定。
		var st int16
		var host sql.NullInt64
		if err := d.QueryRow(`SELECT status,upstream_host_id FROM services WHERE id=$1`, fx.svcID).
			Scan(&st, &host); err != nil {
			t.Fatal(err)
		}
		if st != 0 || host.Int64 != 0 {
			t.Fatalf("未执行恢复不能改服务：status=%d host=%d", st, host.Int64)
		}
	})
	t.Run("有上游账单检查点：拒绝并保持隔离", func(t *testing.T) {
		fx := setupRecoveryEngine(t, d, "renew")
		p := recoveryPayment(d, nil)
		sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
		if err != nil {
			t.Fatalf("读取恢复摘要失败: %v", err)
		}
		if sum.CanResume || sum.ResumeReason == "" {
			t.Fatalf("存在上游账单检查点时应禁止续跑：%+v", sum)
		}
		if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
			recoveryConfirm(fx, "confirmed_not_executed", 0, nil)); err == nil {
			t.Fatal("存在上游账单检查点时未执行恢复应拒绝")
		}
		assertRecoveryIsolated(t, d, fx.svcID)
	})
}

func TestConfirmRecoveryRejects(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		kind   string
		mutate func(*RecoveryConfirmation)
	}{
		{"证据不足十字", "provision", func(r *RecoveryConfirmation) { r.Evidence = "太短" }},
		{"未确认远端稳定", "provision", func(r *RecoveryConfirmation) { r.RemoteStable = false }},
		{"未确认账单已核对", "provision", func(r *RecoveryConfirmation) { r.BillingVerified = false }},
		{"决策非法", "provision", func(r *RecoveryConfirmation) { r.Decision = "executed" }},
		{"版本不匹配", "provision", func(r *RecoveryConfirmation) { r.ExpectedVersion = 99 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupRecoveryEngine(t, d, tc.kind)
			p := recoveryPayment(d, nil)
			err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
				recoveryConfirm(fx, "confirmed_not_executed", 0, tc.mutate))
			if err == nil {
				t.Fatal("应拒绝")
			}
			assertRecoveryIsolated(t, d, fx.svcID)
		})
	}

	t.Run("阻断原因：订单已取消", func(t *testing.T) {
		fx := setupRecoveryEngine(t, d, "renew")
		p := recoveryPayment(d, nil)
		if _, err := d.Exec(`UPDATE orders SET status=2 WHERE id=$1`, fx.orderID); err != nil {
			t.Fatal(err)
		}
		sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
		if err != nil {
			t.Fatalf("读取恢复摘要失败: %v", err)
		}
		if sum.BlockReason == "" {
			t.Fatal("订单已取消应产生阻断原因")
		}
		err = p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
			recoveryConfirm(fx, "confirmed_completed", 555, nil))
		if err == nil {
			t.Fatal("有阻断原因应拒绝")
		}
		if !errors.Is(err, ErrRecoveryConflict) {
			t.Fatalf("应返回隔离冲突错误：%v", err)
		}
		assertRecoveryIsolated(t, d, fx.svcID)
	})
}

func TestConfirmRecoveryBlockedByPendingJobs(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRecoveryEngine(t, d, "provision")
	// 同服务另有一条排队任务：一次只处理一条隔离任务，防止并发恢复互相覆盖。
	if _, err := d.Exec(
		`INSERT INTO fulfillment_jobs(service_id,kind,cycle,status,claim_version,dedupe_key)
		 VALUES($1,'renew','monthly','queued',0,$2)`,
		fx.svcID, "rcveng-pending:"+time.Now().Format("150405.000000000")); err != nil {
		t.Fatal(err)
	}
	p := recoveryPayment(d, nil)
	sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
	if err != nil {
		t.Fatalf("读取恢复摘要失败: %v", err)
	}
	if sum.PendingJobs != 2 || sum.BlockReason == "" {
		t.Fatalf("未决任务>1 应产生阻断原因（隔离任务自身也计入 pending，实得 PendingJobs=%d）：%+v", sum.PendingJobs, sum)
	}
	if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "203.0.113.9:1234",
		recoveryConfirm(fx, "confirmed_completed", 777, nil)); err == nil {
		t.Fatal("存在阻断原因应拒绝")
	}
	assertRecoveryIsolated(t, d, fx.svcID)
}

func TestRecoveryRetryHistory(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	for _, kind := range []string{"provision", "renew"} {
		for _, legacy := range []bool{false, true} {
			for _, oldStatus := range []string{"manual_review", "dead"} {
				t.Run(fmt.Sprintf("%s/历史=%v/%s", kind, legacy, oldStatus), func(t *testing.T) {
					fx := setupRecoveryEngine(t, d, kind)
					oldID := fx.jobID
					if _, err := d.Exec(`UPDATE fulfillment_jobs SET recovery_required=false,status=$2,last_error='原始涨价或失败证据' WHERE id=$1`, oldID, oldStatus); err != nil {
						t.Fatal(err)
					}
					var oldUpdated time.Time
					if err := d.QueryRow(`SELECT updated_at FROM fulfillment_jobs WHERE id=$1`, oldID).Scan(&oldUpdated); err != nil {
						t.Fatal(err)
					}
					p := recoveryPayment(d, nil)
					p.Jobs = repo.NewFulfillmentJobs(d)
					if legacy {
						var order any = fx.orderID
						if kind == "provision" {
							order = nil
						}
						if err := d.QueryRow(`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,dedupe_key) VALUES($1,$2,$3,'monthly',$4) RETURNING id`, fx.svcID, order, kind, fmt.Sprintf("retry:%s:%d:%d", kind, fx.svcID, time.Now().UnixNano())).Scan(&fx.jobID); err != nil {
							t.Fatal(err)
						}
					} else {
						p.TriggerFulfillment = nil
						var err error
						if kind == "provision" {
							err = p.RetryProvision(ctx, fx.svcID)
						} else {
							err = p.RetryRenew(ctx, fx.svcID)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					job, err := p.Jobs.Claim(ctx, time.Minute)
					if err != nil || job == nil {
						t.Fatalf("领取人工重试失败：%+v，%v", job, err)
					}
					fx.jobID = job.ID
					if err := p.Jobs.MarkManualReview(ctx, job, repo.ErrFulfillmentRecoveryRequired, true); err != nil {
						t.Fatal(err)
					}
					sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
					if err != nil || sum.PendingJobs != 1 || sum.BlockReason != "" {
						t.Fatalf("同业务重试历史不应阻断恢复：%+v，%v", sum, err)
					}
					if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "", recoveryConfirm(fx, "confirmed_completed", 555, nil)); err != nil {
						t.Fatal(err)
					}
					var status, message string
					var supersededBy int64
					var updated time.Time
					if err := d.QueryRow(`SELECT status,last_error,superseded_by,updated_at FROM fulfillment_jobs WHERE id=$1`, oldID).Scan(&status, &message, &supersededBy, &updated); err != nil {
						t.Fatal(err)
					}
					if status != "dead" || message != "原始涨价或失败证据" || supersededBy != fx.jobID || !updated.Equal(oldUpdated) {
						t.Fatalf("旧任务应终止且保留原文和接替关系：%s，%s，%d", status, message, supersededBy)
					}
					if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "", recoveryConfirm(fx, "confirmed_completed", 555, nil)); !errors.Is(err, ErrRecoveryConflict) {
						t.Fatalf("重复确认应拒绝：%v", err)
					}
					var jobs, logs, grants int
					if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&jobs); err != nil {
						t.Fatal(err)
					}
					if err := d.QueryRow(`SELECT count(*) FROM admin_logs WHERE target_id=$1 AND action='fulfillment_recovery'`, fx.svcID).Scan(&logs); err != nil {
						t.Fatal(err)
					}
					if err := d.QueryRow(`SELECT count(*) FROM service_period_grants WHERE service_id=$1`, fx.svcID).Scan(&grants); err != nil {
						t.Fatal(err)
					}
					wantGrants := 0
					if kind == "renew" {
						wantGrants = 1
						var expiry string
						if err := d.QueryRow(`SELECT to_char(expires_at,'YYYY-MM-DD') FROM services WHERE id=$1`, fx.svcID).Scan(&expiry); err != nil || expiry != "2027-06-15" {
							t.Fatalf("恢复不能重复发周期：%s，%v", expiry, err)
						}
					}
					if jobs != 2 || logs != 1 || grants != wantGrants {
						t.Fatalf("重复恢复不能删除历史或增加审计/周期：%d/%d/%d", jobs, logs, grants)
					}
				})
			}
		}
	}
}

func TestRecoveryRepeatedManualRetries(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, decision := range []string{"confirmed_completed", "confirmed_not_executed"} {
			t.Run(fmt.Sprintf("历史=%v/%s", legacy, decision), func(t *testing.T) {
				d := testDB(t)
				fx := setupRecoveryEngine(t, d, "provision")
				ctx := context.Background()
				triggered := 0
				p := recoveryPayment(d, &triggered)
				p.Jobs = repo.NewFulfillmentJobs(d)
				if _, err := d.Exec(`UPDATE fulfillment_jobs SET recovery_required=false,last_error='原失败证据' WHERE id=$1`, fx.jobID); err != nil {
					t.Fatal(err)
				}
				var history []int64
				for i := 0; i < 3; i++ {
					history = append(history, fx.jobID)
					if legacy {
						if err := d.QueryRow(`INSERT INTO fulfillment_jobs(service_id,kind,cycle,dedupe_key) VALUES($1,'provision','monthly',$2) RETURNING id`, fx.svcID, fmt.Sprintf("retry:provision:%d:%d", fx.svcID, time.Now().UnixNano())).Scan(&fx.jobID); err != nil {
							t.Fatal(err)
						}
					} else if err := p.RetryProvision(ctx, fx.svcID); err != nil {
						t.Fatal(err)
					}
					job, err := p.Jobs.Claim(ctx, time.Minute)
					if err != nil || job == nil {
						t.Fatalf("领取第%d次人工重试失败：%+v，%v", i+1, job, err)
					}
					fx.jobID = job.ID
					cause := errors.New("原失败证据")
					if i == 2 {
						cause = repo.ErrFulfillmentRecoveryRequired
					}
					if err := p.Jobs.MarkManualReview(ctx, job, cause, i == 2); err != nil {
						t.Fatal(err)
					}
				}
				sum, err := p.FulfillmentRecovery(ctx, fx.svcID)
				if err != nil || sum.BlockReason != "" || sum.PendingJobs != 1 {
					t.Fatalf("重复人工重试链应可恢复：%+v，%v", sum, err)
				}
				before := triggered
				host := int64(555)
				if decision == "confirmed_not_executed" {
					host = 0
				}
				if err := p.ConfirmFulfillmentRecovery(ctx, 9, fx.svcID, "", recoveryConfirm(fx, decision, host, nil)); err != nil {
					t.Fatal(err)
				}
				for i, id := range history {
					want := fx.jobID
					if !legacy && i+1 < len(history) {
						want = history[i+1]
					}
					var status, message string
					var by int64
					if err := d.QueryRow(`SELECT status,last_error,superseded_by FROM fulfillment_jobs WHERE id=$1`, id).Scan(&status, &message, &by); err != nil {
						t.Fatal(err)
					}
					if status != "dead" || message != "原失败证据" || by != want {
						t.Fatalf("接替链和失败原文丢失：%d/%s/%s/%d，期望%d", id, status, message, by, want)
					}
				}
				if decision == "confirmed_not_executed" {
					if triggered != before+1 {
						t.Fatal("原地续跑应触发一次")
					}
					job, err := p.Jobs.Claim(ctx, time.Minute)
					if err != nil || job == nil || job.ID != fx.jobID {
						t.Fatalf("只能领取最后恢复的任务：%+v，%v", job, err)
					}
					if err := p.Jobs.Complete(ctx, job); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestRecoveryRetryHistoryConflicts(t *testing.T) {
	cases := []struct{ name, oldChange, retryKey string }{
		{"仍在排队", "status='queued'", ""},
		{"仍在运行", "status='running'", ""},
		{"自动重试", "status='retry'", ""},
		{"未知结果", "status='manual_review',recovery_required=true", ""},
		{"缺失订单", "order_id=NULL", ""},
		{"其他订单", "status='dead'", ""},
		{"其他周期", "cycle='annually'", ""},
		{"其他业务", "kind='upgrade'", ""},
		{"晚于重试的失败", "updated_at=now()+interval '1 day'", ""},
		{"残留租约", "lease_until=now()+interval '1 day'", ""},
		{"非人工重试", "status='dead'", "ordinary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := testDB(t)
			fx := setupRecoveryEngine(t, d, "renew")
			oldID := fx.jobID
			if tc.name == "其他订单" {
				var otherOrder int64
				if err := d.QueryRow(`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,service_id,kind) SELECT user_id,product_id,priceset_id,cycle,amount,status,service_id,kind FROM orders WHERE id=$1 RETURNING id`, fx.orderID).Scan(&otherOrder); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := d.Exec(`DELETE FROM orders WHERE id=$1`, otherOrder); err != nil {
						t.Error(err)
					}
				})
				if _, err := d.Exec(`UPDATE fulfillment_jobs SET order_id=$2 WHERE id=$1`, oldID, otherOrder); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Exec(`UPDATE fulfillment_jobs SET recovery_required=false,status='dead',last_error='原失败证据' WHERE id=$1`, oldID); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE fulfillment_jobs SET `+tc.oldChange+` WHERE id=$1`, oldID); err != nil {
				t.Fatal(err)
			}
			if tc.name != "非人工重试" {
				if err := repo.NewFulfillmentJobs(d).EnqueueRetry(context.Background(), fx.svcID, fx.orderID, "renew", "monthly"); err == nil {
					t.Fatal("人工入队也不能吞掉冲突历史")
				}
				var count int
				if err := d.QueryRow(`SELECT count(*) FROM fulfillment_jobs WHERE service_id=$1`, fx.svcID).Scan(&count); err != nil || count != 1 {
					t.Fatalf("拒绝入队必须完整回滚：%d，%v", count, err)
				}
			}
			key := tc.retryKey
			if key == "" {
				key = fmt.Sprintf("retry:renew:%d:%d", fx.svcID, time.Now().UnixNano())
			} else {
				key += fmt.Sprint(fx.svcID)
			}
			if err := d.QueryRow(`INSERT INTO fulfillment_jobs(service_id,order_id,kind,cycle,status,recovery_required,claim_version,dedupe_key) VALUES($1,$2,'renew','monthly','manual_review',true,1,$3) RETURNING id`, fx.svcID, fx.orderID, key).Scan(&fx.jobID); err != nil {
				t.Fatal(err)
			}
			// 即使错误原文伪装接替后缀，也不能代替结构化关系或时间证据。
			if _, err := d.Exec(`UPDATE fulfillment_jobs SET last_error=$2 WHERE id=$1`, oldID, fmt.Sprintf("原失败证据\n已由履约任务#%d接替（原任务未成功）", fx.jobID)); err != nil {
				t.Fatal(err)
			}
			p := recoveryPayment(d, nil)
			sum, err := p.FulfillmentRecovery(context.Background(), fx.svcID)
			if err != nil || sum.BlockReason == "" || sum.PendingJobs != 2 {
				t.Fatalf("冲突历史必须阻断：%+v，%v", sum, err)
			}
			if err := p.ConfirmFulfillmentRecovery(context.Background(), 9, fx.svcID, "", recoveryConfirm(fx, "confirmed_completed", 555, nil)); !errors.Is(err, ErrRecoveryConflict) {
				t.Fatalf("不能吞掉冲突历史：%v", err)
			}
			var by sql.NullInt64
			if err := d.QueryRow(`SELECT superseded_by FROM fulfillment_jobs WHERE id=$1`, oldID).Scan(&by); err != nil || by.Valid {
				t.Fatalf("失败恢复不能写接替关系：%v，%v", by, err)
			}
			var isolated bool
			if err := d.QueryRow(`SELECT recovery_required AND status='manual_review' FROM fulfillment_jobs WHERE id=$1`, fx.jobID).Scan(&isolated); err != nil || !isolated {
				t.Fatalf("被恢复任务必须保持隔离：%v，%v", isolated, err)
			}
		})
	}
}

// AdminList 需带回隔离任务的 recovery/recovery_kind/recovery_version，供后台列表渲染恢复入口。
func TestAdminListCarriesRecoveryFields(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	fx := setupRecoveryEngine(t, d, "provision")
	rows, err := NewServicesRepo(d).AdminList(ctx, AdminServiceFilter{Status: -1})
	if err != nil {
		t.Fatal(err)
	}
	var found *AdminServiceRow
	for i := range rows {
		if rows[i].ID == fx.svcID {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("列表应包含测试服务")
	}
	if !found.RecoveryRequired || found.RecoveryKind != "provision" || found.RecoveryVersion != 1 {
		t.Fatalf("应带回隔离任务信息：%v %q %d", found.RecoveryRequired, found.RecoveryKind, found.RecoveryVersion)
	}
}
