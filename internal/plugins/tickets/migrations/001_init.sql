-- tickets 插件：工单全部数据表幂等声明（核心迁移 046/048/049 已建同构表；
-- 此处插件自包含：已存在空跑，缺列补齐，新装等价）。
CREATE TABLE IF NOT EXISTS tickets (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  priority TEXT NOT NULL DEFAULT 'normal',
  category TEXT NOT NULL DEFAULT 'technical',
  status TEXT NOT NULL DEFAULT 'pending',
  service_id BIGINT,
  assignee_admin_id BIGINT,
  closed_at TIMESTAMPTZ,
  closed_reason TEXT NOT NULL DEFAULT '',
  last_reply_by TEXT NOT NULL DEFAULT 'user',
  first_response_at TIMESTAMPTZ,
  timeout_notified_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS service_id BIGINT;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'technical';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS assignee_admin_id BIGINT;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS closed_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS last_reply_by TEXT NOT NULL DEFAULT 'user';
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS first_response_at TIMESTAMPTZ;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS timeout_notified_at TIMESTAMPTZ;
ALTER TABLE tickets ALTER COLUMN status SET DEFAULT 'pending';
CREATE INDEX IF NOT EXISTS idx_tickets_user ON tickets (user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_tickets_service ON tickets (service_id);
CREATE INDEX IF NOT EXISTS idx_tickets_workflow ON tickets(status, priority, assignee_admin_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS ticket_messages (
  id BIGSERIAL PRIMARY KEY,
  ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  user_id BIGINT,
  admin_id BIGINT,
  content TEXT NOT NULL DEFAULT '',
  is_internal BOOLEAN NOT NULL DEFAULT false,
  author_type TEXT NOT NULL DEFAULT 'user',
  read_at TIMESTAMPTZ,
  read_by_user_at TIMESTAMPTZ,
  read_by_admin_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS is_internal BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS author_type TEXT NOT NULL DEFAULT 'user';
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS read_by_user_at TIMESTAMPTZ;
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS read_by_admin_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON ticket_messages (ticket_id, id);
CREATE INDEX IF NOT EXISTS idx_ticket_unread_user ON ticket_messages(ticket_id, author_type, read_by_user_at, read_by_admin_at);

CREATE TABLE IF NOT EXISTS ticket_status_history (
  id BIGSERIAL PRIMARY KEY,
  ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  from_status TEXT NOT NULL DEFAULT '',
  to_status TEXT NOT NULL,
  admin_id BIGINT,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ticket_history_ticket ON ticket_status_history(ticket_id, id);

CREATE TABLE IF NOT EXISTS ticket_attachments (
  id BIGSERIAL PRIMARY KEY,
  ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  message_id BIGINT REFERENCES ticket_messages(id) ON DELETE CASCADE,
  user_id BIGINT,
  admin_id BIGINT,
  storage_ref TEXT NOT NULL,
  original_name TEXT NOT NULL DEFAULT '',
  mime TEXT NOT NULL DEFAULT 'application/octet-stream',
  size BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ticket_attachments_ticket ON ticket_attachments(ticket_id, id);

CREATE TABLE IF NOT EXISTS ticket_assignment_history (
  id BIGSERIAL PRIMARY KEY,
  ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  from_admin_id BIGINT,
  to_admin_id BIGINT,
  changed_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ticket_assignment_ticket ON ticket_assignment_history(ticket_id, id);

-- 工单通知模板默认值（原核心迁移 049/050；新装同样具备，ON CONFLICT 不覆盖站点自定义）。
INSERT INTO settings(key,value) VALUES
  ('ticket_notify_created_title','新工单已提交'),
  ('ticket_notify_created_body','你的工单「{{subject}}」已提交，客服会尽快处理。'),
  ('ticket_notify_reply_title','工单收到新回复'),
  ('ticket_notify_reply_body','你的工单「{{subject}}」收到客服新回复，请登录工单中心查看。'),
  ('ticket_notify_assigned_title','工单已分配'),
  ('ticket_notify_assigned_body','你的工单「{{subject}}」已分配客服处理。'),
  ('ticket_notify_timeout_title','工单处理提醒')
ON CONFLICT (key) DO NOTHING;
