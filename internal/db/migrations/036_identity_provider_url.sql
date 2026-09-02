-- 自动实名任务保存 provider 跳转地址，便于用户刷新页面后继续认证。
ALTER TABLE automatic_identity_attempts ADD COLUMN IF NOT EXISTS provider_url TEXT NOT NULL DEFAULT '';
