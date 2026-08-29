ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS verify_token TEXT NOT NULL DEFAULT '';
-- 存量用户视为已验证，避免升级后无法登录
UPDATE users SET email_verified=true WHERE email_verified=false;
