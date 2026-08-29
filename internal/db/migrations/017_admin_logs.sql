CREATE TABLE admin_logs (
  id          BIGSERIAL PRIMARY KEY,
  admin_id    BIGINT        NOT NULL DEFAULT 0,
  action      TEXT          NOT NULL,
  target_type TEXT          NOT NULL DEFAULT '',
  target_id   BIGINT        NOT NULL DEFAULT 0,
  detail      TEXT          NOT NULL DEFAULT '',
  ip          TEXT          NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_logs_created ON admin_logs (created_at DESC);
