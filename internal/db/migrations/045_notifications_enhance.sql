-- 站内消息增强：分类，便于消息中心筛选与顶栏聚合。
ALTER TABLE notifications
  ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'system';

CREATE INDEX IF NOT EXISTS idx_notifications_user_category
  ON notifications (user_id, category, read, id DESC);
