CREATE TABLE login_attempts (
  key          TEXT PRIMARY KEY,            -- 邮箱或 IP
  attempts     INT           NOT NULL DEFAULT 0,
  first_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
  locked_until TIMESTAMPTZ
);

ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false;
