-- refund 插件 002：商品级退款规则表（version 键 plugin/refund/002_product_rules.sql）
-- product_id=0 表示全局默认规则；精确匹配优先，回退全局。

CREATE TABLE IF NOT EXISTS plugin_refund_product_rules (
  id                    BIGSERIAL PRIMARY KEY,
  product_id            BIGINT       NOT NULL DEFAULT 0,
  refund_requirement    TEXT         NOT NULL DEFAULT 'unlimited', -- unlimited|first_order|first_order_of_product
  window_type           TEXT         NOT NULL DEFAULT 'hours',      -- days|hours
  window_value          INT          NOT NULL DEFAULT 0,            -- 0=不限
  refund_rule           TEXT         NOT NULL DEFAULT 'daily',      -- daily|full
  refund_type           TEXT         NOT NULL DEFAULT 'balance',    -- balance|balance_gateway|gateway_record
  review_mode           TEXT         NOT NULL DEFAULT 'manual',     -- manual|auto
  auto_approve_max      NUMERIC(12,2) NOT NULL DEFAULT 0,           -- 自动通过金额阈值（元），0=不自动
  gateway_fee_rate      NUMERIC(5,2) NOT NULL DEFAULT 0,            -- 原路退款手续费百分比
  gateway_fee_min       NUMERIC(12,2) NOT NULL DEFAULT 0,           -- 最低手续费（元）
  post_refund_action    TEXT         NOT NULL DEFAULT 'none',       -- none|suspend|terminate
  created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_refund_rules_product ON plugin_refund_product_rules (product_id);
