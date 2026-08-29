CREATE TABLE refunds (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT        NOT NULL,
  order_id   BIGINT        NOT NULL,
  invoice_id BIGINT,
  amount     TEXT          NOT NULL,
  method     TEXT          NOT NULL DEFAULT 'balance', -- balance | gateway
  reason     TEXT          NOT NULL DEFAULT '',
  admin_id   BIGINT        NOT NULL DEFAULT 0,
  status     TEXT          NOT NULL DEFAULT 'done',
  created_at TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX idx_refunds_user ON refunds (user_id);
CREATE INDEX idx_refunds_order ON refunds (order_id);
