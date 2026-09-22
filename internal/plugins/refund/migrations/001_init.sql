-- refund 插件：用户自助退款申请（version 键 plugin/refund/001_init.sql）

CREATE TABLE IF NOT EXISTS plugin_refund_requests (
  id          BIGSERIAL PRIMARY KEY,
  order_id    BIGINT       NOT NULL,
  user_id     BIGINT       NOT NULL,
  amount      TEXT         NOT NULL,
  reason      TEXT         NOT NULL DEFAULT '',
  detail      TEXT         NOT NULL DEFAULT '',
  method      TEXT         NOT NULL DEFAULT 'balance',   -- balance | gateway
  status      TEXT         NOT NULL DEFAULT 'pending',   -- pending | approved | rejected | withdrawn
  handle_note TEXT         NOT NULL DEFAULT '',
  handled_by  BIGINT,
  handled_at  TIMESTAMPTZ,
  refund_id   BIGINT,
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- 同一订单同时只允许一条进行中的申请（仿核心部分唯一索引）
CREATE UNIQUE INDEX IF NOT EXISTS uq_plugin_refund_requests_pending ON plugin_refund_requests (order_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_plugin_refund_requests_user ON plugin_refund_requests (user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_plugin_refund_requests_status ON plugin_refund_requests (status, id DESC);
