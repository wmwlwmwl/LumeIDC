CREATE TABLE notifications (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT        NOT NULL,
  title      TEXT          NOT NULL DEFAULT '',
  body       TEXT          NOT NULL DEFAULT '',
  read       BOOLEAN       NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications (user_id, read);
