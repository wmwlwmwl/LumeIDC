-- EasyPanel 续费是本地管控：Provider.Renew 不调用上游，也不创建账单，
-- 因进程中断被 recoverExpired 隔离时无需人工核对，直接恢复为可重试。
-- 收口历史上已经被错误标记为 recovery_required 的 EasyPanel 续费任务。
UPDATE fulfillment_jobs j
SET status='retry', recovery_required=false, lease_until=NULL,
    last_error='EasyPanel 续费任务中断；EasyPanel 续费无需上游账单，已自动安排重试',
    next_attempt_at=now(), updated_at=now()
FROM services s
WHERE s.id=j.service_id
  AND s.upstream_provider='easypanel'
  AND j.kind='renew'
  AND j.status='manual_review'
  AND j.recovery_required=true;

-- 这些服务的 renew_pending/provision_error 是旧 recovery 写入的通用提示；
-- 重试恢复后清掉，避免继续显示人工待办。
UPDATE services s
SET provision_error='', transition_state=''
WHERE s.upstream_provider='easypanel'
  AND s.transition_state='renew_pending'
  AND NOT EXISTS (
    SELECT 1 FROM fulfillment_jobs j
    WHERE j.service_id=s.id AND j.status='manual_review' AND j.recovery_required
  );
