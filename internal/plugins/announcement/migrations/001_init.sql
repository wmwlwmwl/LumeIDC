-- announcement 插件：公告表幂等声明（核心迁移 024/043 已建同构表；此处插件自包含，
-- 新装/老库均安全：已存在则空跑，缺列则补齐）。
CREATE TABLE IF NOT EXISTS announcements (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  cover TEXT NOT NULL DEFAULT '',
  hidden BOOLEAN NOT NULL DEFAULT false,   -- false=显示 true=隐藏
  pinned BOOLEAN NOT NULL DEFAULT false,
  reads INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE announcements
  ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cover TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS reads INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_announcements_category ON announcements (category);
