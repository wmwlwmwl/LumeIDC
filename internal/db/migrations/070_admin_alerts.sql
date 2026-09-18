-- 管理员告警邮件：同一事件只发一次的持久去重账本。
-- 只记录"已决策发送"的告警键，不存邮件内容快照（正文仍走 mail_outbox 投递）。
-- 与 fulfillment_jobs.dedupe_key / sms_outbox.notification_id 一样用唯一键做幂等：
-- 并发下由数据库保证只有一个事务能插入成功，避免重复通知管理员。
CREATE TABLE IF NOT EXISTS admin_alert_log (
  id BIGSERIAL PRIMARY KEY,
  alert_key TEXT NOT NULL,
  category TEXT NOT NULL,
  subject TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS admin_alert_log_key_unique ON admin_alert_log (alert_key);
CREATE INDEX IF NOT EXISTS admin_alert_log_created_idx ON admin_alert_log (created_at DESC);
