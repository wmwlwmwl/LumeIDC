-- 服务器凭据版本号：轮换凭据时递增，使 JWT 缓存自动失效。
ALTER TABLE servers ADD COLUMN IF NOT EXISTS credential_revision INT NOT NULL DEFAULT 0;
