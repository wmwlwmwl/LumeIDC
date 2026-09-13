ALTER TABLE tickets ADD COLUMN IF NOT EXISTS closed_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS first_response_at TIMESTAMPTZ;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS timeout_notified_at TIMESTAMPTZ;
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS author_type TEXT NOT NULL DEFAULT 'user';
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS read_by_user_at TIMESTAMPTZ;
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS read_by_admin_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS ticket_assignment_history (
  id BIGSERIAL PRIMARY KEY,
  ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  from_admin_id BIGINT,
  to_admin_id BIGINT,
  changed_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ticket_assignment_ticket ON ticket_assignment_history(ticket_id, id);
CREATE INDEX IF NOT EXISTS idx_ticket_unread_user ON ticket_messages(ticket_id, author_type, read_by_user_at, read_by_admin_at);

INSERT INTO settings(key,value) VALUES
  ('ticket_notify_created_title','新工单已提交'),
  ('ticket_notify_created_body','你的工单「{{subject}}」已提交，客服会尽快处理。'),
  ('ticket_notify_reply_title','工单收到新回复'),
  ('ticket_notify_reply_body','你的工单「{{subject}}」收到客服新回复，请登录工单中心查看。'),
  ('ticket_notify_assigned_title','工单已分配'),
  ('ticket_notify_assigned_body','你的工单「{{subject}}」已分配客服处理。'),
  ('ticket_notify_timeout_title','工单处理提醒')
ON CONFLICT (key) DO NOTHING;
