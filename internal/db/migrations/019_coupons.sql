CREATE TABLE coupons (
  id          BIGSERIAL PRIMARY KEY,
  code        TEXT          NOT NULL UNIQUE,
  type        TEXT          NOT NULL DEFAULT 'percent', -- percent | fixed
  value       NUMERIC(10,2) NOT NULL DEFAULT 0,
  min_amount  NUMERIC(10,2) NOT NULL DEFAULT 0,
  starts_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ,
  usage_limit INT           NOT NULL DEFAULT 0, -- 0 = 不限次数
  used_count  INT           NOT NULL DEFAULT 0,
  active      BOOLEAN       NOT NULL DEFAULT true
);

CREATE TABLE coupon_usages (
  id         BIGSERIAL PRIMARY KEY,
  coupon_id  BIGINT        NOT NULL,
  user_id    BIGINT        NOT NULL,
  order_id   BIGINT        NOT NULL,
  discount   NUMERIC(10,2) NOT NULL DEFAULT 0
);
CREATE INDEX idx_coupon_usages_user ON coupon_usages (user_id);
