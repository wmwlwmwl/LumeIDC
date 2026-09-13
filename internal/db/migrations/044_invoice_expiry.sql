-- 未支付账单过期策略：创建后 24 小时内可支付，过期后进入独立状态 3。
ALTER TABLE invoices
  ADD COLUMN IF NOT EXISTS due_at TIMESTAMPTZ;

UPDATE invoices
SET due_at = created_at + interval '24 hours'
WHERE due_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_invoices_unpaid_due
  ON invoices (due_at)
  WHERE status = 0;
