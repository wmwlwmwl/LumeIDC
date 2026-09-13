-- 042 履约任务支持升级：修正 009 中 fulfillment_jobs.kind 仅允许 provision/renew 的约束。
-- 040 已引入 orders.kind='upgrade' 与 Lifecycle.Upgrade，但遗漏了履约任务表本身的 kind 约束，
-- 导致升级单支付时事务写入 kind='upgrade' 触发 fulfillment_jobs_kind_check 失败（SQLSTATE 23514）。
ALTER TABLE fulfillment_jobs DROP CONSTRAINT IF EXISTS fulfillment_jobs_kind_check;
ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_kind_check
    CHECK (kind IN ('provision','renew','upgrade'));
