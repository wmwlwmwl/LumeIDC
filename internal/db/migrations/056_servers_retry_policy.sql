-- 上游可自愈失败（典型：余额不足）的自动重试策略，按上游实例配置。
--   retry_later_enabled           关闭后这类失败立即转人工复核，不再自动重试
--   retry_later_interval_minutes  重试间隔（分钟）；上游充值到账后最多等这么久自动开通/续费
-- 默认值与此前代码里的硬编码一致（启用 / 10 分钟），升级后行为不变。
ALTER TABLE servers ADD COLUMN IF NOT EXISTS retry_later_enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS retry_later_interval_minutes INT NOT NULL DEFAULT 10;
