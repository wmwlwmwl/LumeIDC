-- 记录上游开通失败原因，展示在后台/用户端服务页
ALTER TABLE services ADD COLUMN IF NOT EXISTS provision_error TEXT NOT NULL DEFAULT '';