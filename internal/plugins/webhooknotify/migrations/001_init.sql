-- webhooknotify 插件：单行配置表 + 投递日志表（version 键 plugin/webhooknotify/001_init.sql）

CREATE TABLE IF NOT EXISTS plugin_webhooknotify_config (
  id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- 单行配置
  url        TEXT NOT NULL DEFAULT '',
  secret     TEXT NOT NULL DEFAULT '',
  events     JSONB NOT NULL DEFAULT '[]',
  enabled    BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO plugin_webhooknotify_config(id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS plugin_webhooknotify_log (
  id          BIGSERIAL PRIMARY KEY,
  event       TEXT NOT NULL,
  payload     JSONB NOT NULL,
  http_status INT NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  duration_ms INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pwn_log_created ON plugin_webhooknotify_log(created_at DESC);
