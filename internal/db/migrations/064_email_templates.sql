-- 仅存覆盖。全空记录表示明确恢复默认，防止旧工单设置再次成为邮件覆盖。
CREATE TABLE IF NOT EXISTS email_templates (
    code TEXT PRIMARY KEY,
    subject TEXT,
    body TEXT,
    enabled BOOLEAN,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 旧任务与旧调用一律按纯文本投递，不通过内容猜测格式。
ALTER TABLE mail_outbox ADD COLUMN IF NOT EXISTS format TEXT NOT NULL DEFAULT 'text';
ALTER TABLE mail_outbox ADD CONSTRAINT mail_outbox_format_check CHECK (format IN ('text','html'));
