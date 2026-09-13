-- 上游停售原因（定时同步专用，与管理员手动 hidden 分开，互不覆盖）：
--   ''         正常在售
--   unshelved  上游可售目录中已不存在（下架/删除）
-- 非空即视为停售：前台不展示、购买页 404、下单拦截；上游恢复后自动置回 ''。
ALTER TABLE products ADD COLUMN IF NOT EXISTS upstream_offline_reason text NOT NULL DEFAULT '';
