-- 添加人工复核状态：settle 成功但 checkpoint 未落库的未知窗口。
ALTER TABLE fulfillment_jobs DROP CONSTRAINT IF EXISTS fulfillment_jobs_status_check;
ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_status_check
    CHECK (status IN ('queued','running','retry','succeeded','dead','manual_review'));
