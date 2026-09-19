package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// Fulfillment executes persisted provision/renew jobs. PostgreSQL leases make
// jobs recoverable after process exit without adding an external queue.
type Fulfillment struct {
	Jobs      *repo.FulfillmentJobs
	Payment   *Payment
	Lifecycle *Lifecycle // renew 用；组合根恒注入，nil 时任务直接报错
	Notifier  *Notifier  // 失败时给管理员发告警邮件；nil 表示不告警
}

// ponytail: 单进程共用 4 个执行槽，按当前 25 连接池预留上游账户锁及结果写回空间；
// 多副本不共享额度，扩容或缩小连接池时应按池容量调整，并改为跨实例配额。
// 满额不排队、不领取，持久任务由后续轮询兜底。
var fulfillmentSlots = make(chan struct{}, 4)

func acquireFulfillment(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case fulfillmentSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (f *Fulfillment) ProcessOne(ctx context.Context) (bool, error) {
	if !acquireFulfillment(ctx) {
		return false, ctx.Err()
	}
	defer func() { <-fulfillmentSlots }()
	return f.processOne(ctx)
}

// processOne 仅由已取得执行槽的入口调用，预算覆盖领取、借连接和上游操作。
func (f *Fulfillment) processOne(ctx context.Context) (bool, error) {
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	job, err := f.Jobs.Claim(opCtx, 3*time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	conn, unlock, err := f.Jobs.TryExecutionLock(opCtx, job.ServiceID)
	if err != nil {
		// 未触及上游，原领取原地延后；不持连接等待执行者退出。
		// 除 busy 外的取锁失败（连接池耗尽、锁查询报错）同样必须退回 retry：
		// 留 running 会在租约到期后被 recoverExpired 判为「上游结果未知」而隔离，
		// 逼管理员去对账一件根本没发出去的上游请求。
		return true, f.deferClaim(ctx, job, err)
	}
	defer unlock()
	if err := f.Jobs.ValidateClaim(opCtx, conn, job); err != nil {
		// 同上：领取权失效或校验查询失败时上游一步都没走，按重试处理而非留 running。
		return true, f.deferClaim(ctx, job, err)
	}
	switch job.Kind {
	case "provision":
		err = f.Payment.provision(opCtx, job.ServiceID, 0, job.Cycle)
	case "renew":
		lc := f.Lifecycle
		if lc == nil {
			err = fmt.Errorf("renew 任务缺少 Lifecycle 服务")
			break
		}
		err = lc.Renew(opCtx, job.ServiceID, job.Cycle, job.OrderID.Int64)
	case "upgrade":
		lc := f.Lifecycle
		if lc == nil {
			err = fmt.Errorf("upgrade 任务缺少 Lifecycle 服务")
			break
		}
		err = lc.Upgrade(opCtx, job.ServiceID, job.Cycle, job.OrderID.Int64)
	default:
		err = fmt.Errorf("未知履约任务类型: %s", job.Kind)
	}
	// 工作预算短于租约；即使调用方取消，也保留最多 30 秒记录结果，不能把超时当作未执行。
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	if opCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// EasyPanel 续费是空操作：不调用上游、不建账单，中断只需原地重试，
		// 不应进入面向真实上游的 recovery_required 隔离。
		if f.isEasyPanelRenew(ctx, job) {
			return true, f.Jobs.RetryLater(ctx, job, repo.ErrEasyPanelRenewInterrupted, time.Minute)
		}
		ferr := f.Jobs.MarkManualReview(ctx, job, repo.ErrFulfillmentRecoveryRequired, true)
		if ferr == nil {
			f.notifyFailure(ctx, job, repo.ErrFulfillmentRecoveryRequired, "结果未知，需人工对账")
		}
		return true, ferr
	}
	if err != nil {
		// 检查是否为人工复核错误
		if server.IsManualReview(err) {
			var unknown *server.ManualReviewError
			if ferr := f.Jobs.MarkManualReview(ctx, job, err, errors.As(err, &unknown)); ferr != nil {
				return true, fmt.Errorf("标记人工复核失败: %w", ferr)
			}
			f.notifyFailure(ctx, job, err, "需人工复核")
			return true, err
		}
		// 等外部条件（上游余额不足）：按该上游配置决定保持重试还是转人工。
		if server.IsRetryLater(err) {
			if ferr := f.markRetryLater(ctx, job, err); ferr != nil {
				return true, fmt.Errorf("记录等待重试状态失败: %w", ferr)
			}
			f.notifyFailure(ctx, job, err, "等外部条件（如上游余额不足）")
			return true, err
		}
		if ferr := f.Jobs.Fail(ctx, job, err); ferr != nil {
			return true, fmt.Errorf("记录履约失败结果: %w", ferr)
		}
		f.notifyFailure(ctx, job, err, "执行失败")
		return true, err
	}
	return true, f.Jobs.Complete(ctx, job)
}

// deferClaim 把「尚未触及上游」的失败退回 retry，避免误判为「上游结果未知」。
//
// 留 running 的任务会在租约（3 分钟）到期后被 recoverExpired 隔离成 manual_review +
// recovery_required，管理员看到的是「请核对上游账单和实例，禁止直接重试」——但对
// 取锁失败、领取权失效这类错误，上游一步都没走，属于纯误判。
//
// 返回值沿用原契约：真实故障原样上报（调用方据此记日志，不能吞掉 DB 错误）；
// 仅 busy 返回 nil——同一服务有执行者/恢复/退款在跑是常态，不该刷错误日志。
// 退回动作本身失败（DB 不可用、领取权已失效）时只能上报原因，此时已无更安全的写入口。
func (f *Fulfillment) deferClaim(ctx context.Context, job *repo.FulfillmentJob, cause error) error {
	writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	if err := f.Jobs.RetryLater(writeCtx, job, cause, time.Minute); err != nil {
		log.Printf("[fulfillment] 任务 %d 退回重试失败，租约到期后会被当作「上游结果未知」隔离: %v（原因: %v）",
			job.ID, err, cause)
		return cause
	}
	if errors.Is(cause, repo.ErrFulfillmentBusy) {
		return nil
	}
	return cause
}

func (f *Fulfillment) isEasyPanelRenew(ctx context.Context, job *repo.FulfillmentJob) bool {
	if f == nil || f.Payment == nil || f.Payment.db == nil || job == nil || job.Kind != "renew" {
		return false
	}
	var provider string
	if err := f.Payment.db.QueryRowContext(ctx, `SELECT coalesce(upstream_provider,'') FROM services WHERE id=$1`, job.ServiceID).Scan(&provider); err != nil {
		return false
	}
	return provider == "easypanel"
}

// notifyFailure 就一次履约失败向管理员发一封告警邮件，同一任务只发一次。
//
// 去重键取任务自身的 dedupe_key（fulfillment_jobs 上唯一）：
//   - 自动任务为「类型:订单号」，同一订单的开通/续费/升降级无论重试多少次只告警一次；
//   - 人工重试入队时带时间戳生成新键，管理员重跑后再次失败会重新告警，不会被旧记录吞掉。
//
// 告警失败只记日志：它不得改变任务本身的失败处置结果。
func (f *Fulfillment) notifyFailure(ctx context.Context, job *repo.FulfillmentJob, cause error, outcome string) {
	if f == nil || f.Notifier == nil || job == nil {
		return
	}
	key := "fulfill:" + job.DedupeKey
	op := f.operationLabel(ctx, job)
	reason := "未知原因"
	if cause != nil {
		reason = strings.TrimSpace(cause.Error())
	}
	orderNo := "—"
	if job.OrderID.Valid && job.OrderID.Int64 > 0 {
		orderNo = strconv.FormatInt(job.OrderID.Int64, 10)
	}
	body := adminAlertFields(
		"服务的「"+op+"」操作失败，需要管理员处理。",
		[2]string{"操作类型", op},
		[2]string{"处理结果", outcome},
		[2]string{"服务编号", strconv.FormatInt(job.ServiceID, 10)},
		[2]string{"订单编号", orderNo},
		[2]string{"周期", job.Cycle},
		[2]string{"失败原因", reason},
		[2]string{"发生时间", time.Now().Format("2006-01-02 15:04:05")},
		[2]string{"处理建议", fulfillmentAdvice(outcome)},
	)
	if err := f.Notifier.NotifyAdminOnce(ctx, key, "fulfillment", op+"失败待处理", body); err != nil {
		log.Printf("[fulfillment] 管理员告警发送失败（service %d, key=%s）: %v", job.ServiceID, key, err)
	}
}

// fulfillmentAdvice 给出与失败处置对应的后台操作提示，避免管理员看到报错不知道下一步做什么。
func fulfillmentAdvice(outcome string) string {
	switch outcome {
	case "结果未知，需人工对账":
		return "先在「服务管理-履约对账」核对上游账单与实例，再决定重试或退款"
	case "等外部条件（如上游余额不足）":
		return "多为上游账户余额不足，给上游充值后会自动重试；也可立即在后台手动重试"
	default:
		return "在「服务管理」查看该服务的失败原因列，确认后手动重试或退款"
	}
}

// operationLabel 把任务类型翻译成业务用语；升降级共用 upgrade 任务，
// 靠升级订单的有符号差价区分方向（diff_amount<0 为降配）。
func (f *Fulfillment) operationLabel(ctx context.Context, job *repo.FulfillmentJob) string {
	switch job.Kind {
	case "provision":
		return "开通"
	case "renew":
		return "续费"
	case "upgrade":
		if f.isDowngrade(ctx, job) {
			return "降配"
		}
		return "升配"
	default:
		return job.Kind
	}
}

// isDowngrade 读取升级订单的差价判断降配。查不到（无订单号、Payment 未注入、查询失败）
// 时按升配处理——仅在邮件里区分方向，不能因此影响任务本身的处置。
func (f *Fulfillment) isDowngrade(ctx context.Context, job *repo.FulfillmentJob) bool {
	if f == nil || f.Payment == nil || f.Payment.db == nil {
		return false
	}
	if !job.OrderID.Valid || job.OrderID.Int64 <= 0 {
		return false
	}
	var diff float64
	if err := f.Payment.db.QueryRowContext(ctx,
		`SELECT coalesce(diff_amount,0)::float8 FROM orders WHERE id=$1`, job.OrderID.Int64).Scan(&diff); err != nil {
		return false
	}
	return diff < 0
}

// markRetryLater 处理"等外部条件"类失败：按该服务所属上游的配置，
// 决定保持自动重试（不消耗重试次数）还是立即转人工复核。
// 策略在 service 层而非 provider：provider 只负责陈述"这个失败等外部条件"（RetryLaterError），
// 新上游接入时无需感知重试配置，配置自动生效。
func (f *Fulfillment) markRetryLater(ctx context.Context, job *repo.FulfillmentJob, cause error) error {
	enabled, minutes := true, repo.DefaultRetryLaterMinutes
	if f.Lifecycle != nil && f.Lifecycle.Servers != nil {
		e, m, err := f.Lifecycle.Servers.RetryLaterPolicy(ctx, job.ServiceID)
		if err != nil {
			log.Printf("[fulfillment] 读取上游重试策略失败（service %d），按默认值处理: %v", job.ServiceID, err)
		} else {
			enabled, minutes = e, m
		}
	}
	// 该上游关掉了自动等待：立即转人工，由管理员充值后手动重试。
	if !enabled {
		return f.Jobs.MarkManualReview(ctx, job, cause, false)
	}
	if minutes <= 0 { // 边界防护：间隔被写成 0/负数会变成高频空转
		minutes = repo.DefaultRetryLaterMinutes
	}
	return f.Jobs.RetryLater(ctx, job, cause, time.Duration(minutes)*time.Minute)
}

// TriggerDrain 在创建协程前非阻塞取得额度，不为已持久化的积压另建内存队列。
func (f *Fulfillment) TriggerDrain(ctx context.Context, limit int) {
	if limit <= 0 || !acquireFulfillment(ctx) {
		return
	}
	go func() {
		defer func() { <-fulfillmentSlots }()
		f.drain(ctx, limit)
	}()
}

func (f *Fulfillment) Drain(ctx context.Context, limit int) {
	if limit <= 0 || !acquireFulfillment(ctx) {
		return
	}
	defer func() { <-fulfillmentSlots }()
	f.drain(ctx, limit)
}

func (f *Fulfillment) drain(ctx context.Context, limit int) {
	for i := 0; i < limit && ctx.Err() == nil; i++ {
		didWork, err := f.processOne(ctx)
		if err != nil {
			log.Printf("[fulfillment] 任务失败: %v", err)
		}
		if !didWork {
			return
		}
	}
}
