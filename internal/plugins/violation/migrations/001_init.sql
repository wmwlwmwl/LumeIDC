-- violation 插件：用户违规记录 + 违规公示公告（version 键 plugin/violation/001_init.sql）

CREATE TABLE IF NOT EXISTS plugin_violation_records (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT       NOT NULL,
  type         TEXT         NOT NULL DEFAULT '',           -- 违规类型（配置项 typeOptions）
  level        TEXT         NOT NULL DEFAULT 'light',      -- light | medium | severe（固定枚举）
  description  TEXT         NOT NULL DEFAULT '',
  evidence_url TEXT         NOT NULL DEFAULT '',
  action       TEXT         NOT NULL DEFAULT 'warn',       -- 处置措施（配置项 actionOptions）
  starts_at    TIMESTAMPTZ,                                 -- 生效时间（空=立即生效）
  expires_at   TIMESTAMPTZ,                                 -- 过期时间（空=长期有效）
  public       BOOLEAN      NOT NULL DEFAULT false,        -- 是否前台公示
  handled_by   BIGINT       NOT NULL DEFAULT 0,             -- 记录人（管理员 ID）
  note         TEXT         NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_plugin_violation_records_user ON plugin_violation_records (user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_plugin_violation_records_public ON plugin_violation_records (public, id DESC);

CREATE TABLE IF NOT EXISTS plugin_violation_announcements (
  id         BIGSERIAL PRIMARY KEY,
  title      TEXT         NOT NULL DEFAULT '',
  content    TEXT         NOT NULL DEFAULT '',
  pinned     BOOLEAN      NOT NULL DEFAULT false,
  hidden     BOOLEAN      NOT NULL DEFAULT false,           -- false=显示 true=隐藏
  reads      BIGINT       NOT NULL DEFAULT 0,
  created_by BIGINT       NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_plugin_violation_ann_list ON plugin_violation_announcements (hidden, pinned DESC, id DESC);
