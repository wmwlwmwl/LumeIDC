package service

import (
	"context"
	"errors"
	"fmt"
	"log"
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
}

func (f *Fulfillment) ProcessOne(ctx context.Context) (bool, error) {
	job, err := f.Jobs.Claim(ctx, 3*time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	_, unlock, err := f.Jobs.TryExecutionLock(ctx, job.ServiceID)
	if err != nil {
		// 未触及上游，原领取原地延后；不持连接等待执行者退出。
		if errors.Is(err, repo.ErrFulfillmentBusy) {
			return true, f.Jobs.RetryLater(ctx, job, err, time.Minute)
		}
		return true, err
	}
	defer unlock()
	if err := f.Jobs.ValidateClaim(ctx, job); err != nil {
		return true, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
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
	// 工作预算短于租约，留出结果落库时间；超时不能证明上游没有执行。
	if opCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true, f.Jobs.MarkManualReview(ctx, job, repo.ErrFulfillmentRecoveryRequired, true)
	}
	if err != nil {
		// 检查是否为人工复核错误
		if server.IsManualReview(err) {
			var unknown *server.ManualReviewError
			if ferr := f.Jobs.MarkManualReview(ctx, job, err, errors.As(err, &unknown)); ferr != nil {
				return true, fmt.Errorf("标记人工复核失败: %w", ferr)
			}
			return true, err
		}
		// 等外部条件（上游余额不足）：按该上游配置决定保持重试还是转人工。
		if server.IsRetryLater(err) {
			if ferr := f.markRetryLater(ctx, job, err); ferr != nil {
				return true, fmt.Errorf("记录等待重试状态失败: %w", ferr)
			}
			return true, err
		}
		if ferr := f.Jobs.Fail(ctx, job, err); ferr != nil {
			return true, fmt.Errorf("记录履约失败结果: %w", ferr)
		}
		return true, err
	}
	return true, f.Jobs.Complete(ctx, job)
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

func (f *Fulfillment) Drain(ctx context.Context, limit int) {
	for i := 0; i < limit; i++ {
		didWork, err := f.ProcessOne(ctx)
		if err != nil {
			log.Printf("[fulfillment] 任务失败: %v", err)
		}
		if !didWork {
			return
		}
	}
}
