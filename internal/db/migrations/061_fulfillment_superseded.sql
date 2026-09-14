-- 人工重试只接替同业务的已停止任务；保留失败原文，不伪装成成功。
ALTER TABLE fulfillment_jobs ADD COLUMN superseded_by BIGINT REFERENCES fulfillment_jobs(id);
ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_superseded_check
    CHECK (superseded_by IS NULL OR (superseded_by > id AND status='dead' AND NOT recovery_required AND lease_until IS NULL));
-- 默认 NO ACTION 保留接替证据：不能单删接替者；整条服务链仍可级联清理。
CREATE INDEX idx_fulfillment_jobs_superseded ON fulfillment_jobs(superseded_by) WHERE superseded_by IS NOT NULL;
