-- 仅记录新通知的邮件意图；不回填历史通知，不关联可被删除的站内信。
CREATE TABLE mail_outbox (
  id BIGSERIAL PRIMARY KEY,
  recipient TEXT NOT NULL,
  subject TEXT NOT NULL,
  body TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running')),
  attempts INTEGER NOT NULL DEFAULT 0,
  version BIGINT NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_until TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((state='running') = (lease_until IS NOT NULL))
);
CREATE INDEX idx_mail_outbox_pending ON mail_outbox (available_at,id) WHERE state='pending';
CREATE INDEX idx_mail_outbox_lease ON mail_outbox (lease_until,id) WHERE state='running';
