package service

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

// ponytail: 单实例固定 2 个后台发送者，共享 4 个 SMTP 连接额度，至少留 2 个给同步验证码/测试。
// 多实例需要按部署总数分配额度，或升级为共享限流；当前不提供跨实例 SMTP 总量保证。
const (
	mailWorkers      = 2
	mailConnections  = 4
	mailPollInterval = 5 * time.Second
	mailSendTimeout  = 45 * time.Second
	mailLease        = 2 * time.Minute // 大于整封发送截止与数据库写回预算，无需续租。
)

var smtpSlots = make(chan struct{}, mailConnections)

func (n *Notifier) initMail() {
	n.initOnce.Do(func() {
		n.mailCtx, n.mailCancel = context.WithCancel(context.Background())
		n.wake = make(chan struct{}, 1)
		n.done = make(chan struct{})
	})
}

func (n *Notifier) wakeMail() {
	n.initMail()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

func (n *Notifier) StartMail() {
	n.initMail()
	n.startOnce.Do(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.stopped {
			return
		}
		n.workers.Add(mailWorkers)
		for i := 0; i < mailWorkers; i++ {
			go n.mailWorker()
		}
	})
}

// StopMail 先停止领取并取消所有实际发送，等待受 ctx 限制；未写回的租约由下次启动回收。
func (n *Notifier) StopMail(ctx context.Context) error {
	if n == nil {
		return nil
	}
	n.initMail()
	n.stopOnce.Do(func() {
		n.mu.Lock()
		n.stopped = true
		n.mailCancel()
		n.mu.Unlock()
		go func() {
			n.workers.Wait()
			close(n.done)
		}()
	})
	select {
	case <-n.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type mailJob struct {
	id, version       int64
	attempts          int
	to, subject, body string
	format            string
}

// claimMail 单条语句领取，过期 running 同样参与；版本令牌阻止旧发送者覆盖新租约。
func (n *Notifier) claimMail(ctx context.Context) (mailJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var job mailJob
	err := n.db.QueryRowContext(ctx, `WITH candidate AS (
		SELECT id FROM mail_outbox
		WHERE (state='pending' AND available_at<=now()) OR (state='running' AND lease_until<=now())
		ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE mail_outbox m SET state='running',lease_until=now()+$1::interval,
		version=m.version+1,attempts=LEAST(m.attempts+1,30)
	FROM candidate c WHERE m.id=c.id
	RETURNING m.id,m.version,m.attempts,m.recipient,m.subject,m.body,m.format`, mailLease.String()).Scan(
		&job.id, &job.version, &job.attempts, &job.to, &job.subject, &job.body, &job.format)
	return job, err
}

func mailRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return min(30*time.Second*time.Duration(1<<uint(attempt-1)), time.Hour)
}

// finishMail 成功删除快照，失败保留并退避。SMTP 接受后崩溃仍可能重复，语义为至少一次。
func (n *Notifier) finishMail(ctx context.Context, job mailJob, sent bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if sent {
		_, err := n.db.ExecContext(ctx, `DELETE FROM mail_outbox WHERE id=$1 AND version=$2 AND state='running'`, job.id, job.version)
		return err
	}
	_, err := n.db.ExecContext(ctx, `UPDATE mail_outbox SET state='pending',lease_until=NULL,
		available_at=now()+$3::interval WHERE id=$1 AND version=$2 AND state='running'`, job.id, job.version, mailRetryDelay(job.attempts).String())
	return err
}

func (n *Notifier) mailWorker() {
	defer n.workers.Done()
	ticker := time.NewTicker(mailPollInterval)
	defer ticker.Stop()
	for {
		if n.mailCtx.Err() != nil {
			return
		}
		job, err := n.claimMail(n.mailCtx)
		if err == nil {
			// 合并唤醒仍允许另一个空闲 worker 参与，不为每封邮件建立 goroutine。
			n.wakeMail()
			sendErr := n.sendMailFormat(n.mailCtx, job.to, job.subject, job.body, job.format)
			// 停机也尝试短时写回；失败则保留 running 等租约回收。
			if err := n.finishMail(context.Background(), job, sendErr == nil); err != nil {
				log.Printf("邮件队列写回失败，任务编号=%d，将在租约到期后重试", job.id)
			} else if sendErr != nil && n.mailCtx.Err() == nil {
				log.Printf("邮件暂未发出，任务编号=%d，已保留并延后重试", job.id)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) && n.mailCtx.Err() == nil {
			log.Print("读取待发邮件失败，将在下次轮询重试")
		}
		select {
		case <-n.mailCtx.Done():
			return
		case <-n.wake:
		case <-ticker.C:
		}
	}
}
