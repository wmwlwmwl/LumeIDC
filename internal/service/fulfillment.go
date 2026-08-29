package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// Fulfillment executes persisted provision/renew jobs. PostgreSQL leases make
// jobs recoverable after process exit without adding an external queue.
type Fulfillment struct {
	Jobs    *repo.FulfillmentJobs
	Payment *Payment
}

func (f *Fulfillment) ProcessOne(ctx context.Context) (bool, error) {
	job, err := f.Jobs.Claim(ctx, 3*time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch job.Kind {
	case "provision":
		err = f.Payment.provision(opCtx, job.ServiceID, 0, job.Cycle)
	case "renew":
		lc := &Lifecycle{DB: f.Payment.DB, Servers: f.Payment.Servers, Products: f.Payment.Products}
		err = lc.Renew(opCtx, job.ServiceID, job.Cycle)
	default:
		err = fmt.Errorf("未知履约任务类型: %s", job.Kind)
	}
	if err != nil {
		// 检查是否为人工复核错误
		if server.IsManualReview(err) {
			if ferr := f.Jobs.MarkManualReview(ctx, job.ID, err); ferr != nil {
				return true, fmt.Errorf("标记人工复核失败: %w", ferr)
			}
			return true, err
		}
		if ferr := f.Jobs.Fail(ctx, job.ID, job.Attempts, err); ferr != nil {
			return true, fmt.Errorf("记录履约失败结果: %w", ferr)
		}
		return true, err
	}
	return true, f.Jobs.Complete(ctx, job.ID)
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
