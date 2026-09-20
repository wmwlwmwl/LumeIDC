-- 插件运行时启停状态（无记录 = 默认启用）。
CREATE TABLE IF NOT EXISTS plugins (
  name       TEXT PRIMARY KEY,
  enabled    BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
