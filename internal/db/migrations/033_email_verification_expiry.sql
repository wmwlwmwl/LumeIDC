-- 邮箱验证令牌生命周期：旧库中的空时间视为需要重新注册/重新发送。
ALTER TABLE users ADD COLUMN IF NOT EXISTS verify_token_created_at TIMESTAMPTZ;
