ALTER TABLE invoices ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'order';
CREATE INDEX IF NOT EXISTS idx_invoices_user_kind ON invoices(user_id, kind, id DESC);
