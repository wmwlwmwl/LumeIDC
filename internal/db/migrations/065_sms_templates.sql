CREATE TABLE sms_templates (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('aliyun','aliyun_sms','stay33')),
    kind TEXT NOT NULL CHECK (kind IN ('otp','notification')),
    template_code TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    parameters JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    CHECK (provider <> 'aliyun' OR kind = 'otp')
);
CREATE TABLE sms_scene_bindings (
    code TEXT PRIMARY KEY CHECK (code IN (
        'otp_register','otp_login','otp_reset_password','otp_bind','otp_change','otp_profile_phone_old','otp_verify_phone',
        'ticket_created','ticket_reply','ticket_assigned','ticket_timeout','payment_success','recharge_success','service_expiring',
        'identity_submitted','identity_approved','identity_rejected','cancel_submitted','cancel_approved','cancel_rejected')),
    template_id BIGINT REFERENCES sms_templates(id) ON DELETE RESTRICT,
    enabled BOOLEAN NOT NULL DEFAULT false,
    CHECK (code NOT LIKE 'otp_%' OR (enabled AND template_id IS NOT NULL)),
    CHECK (NOT enabled OR template_id IS NOT NULL)
);
-- 仅记录新通知，不从历史站内信补发；不存密钥、不排队验证码。
CREATE TABLE sms_outbox (
    id BIGSERIAL PRIMARY KEY,
    notification_id BIGINT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL,
    scene TEXT NOT NULL,
    template_id BIGINT NOT NULL,
    recipient TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','sent','failed','unknown','skipped')),
    message TEXT NOT NULL DEFAULT '等待发送',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sms_outbox_pending_idx ON sms_outbox(id) WHERE status='pending';
CREATE INDEX sms_outbox_running_idx ON sms_outbox(lease_until) WHERE status='running';
