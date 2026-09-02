-- 040 服务备注 + 升降级：services.remark/cycle/config_snapshot、orders.kind/target_product_id/diff_amount

ALTER TABLE services ADD COLUMN IF NOT EXISTS remark TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN IF NOT EXISTS cycle TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN IF NOT EXISTS config_snapshot JSONB;

ALTER TABLE orders ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'provision';          -- provision/renew/upgrade
ALTER TABLE orders ADD COLUMN IF NOT EXISTS target_product_id BIGINT REFERENCES products(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS diff_amount NUMERIC(12,2) NOT NULL DEFAULT 0;    -- 有符号差价：>0 补差价，<0 退余额

-- 存量续费单回填 kind（service_id 非空即续费；新购单保持 provision）
UPDATE orders SET kind='renew' WHERE kind='provision' AND service_id IS NOT NULL;

-- 防抖：同服务未支付升级单（复用 CreateRenewOrder 的"复用未付单"模式）
CREATE INDEX IF NOT EXISTS idx_orders_upgrade_pending ON orders(service_id) WHERE kind='upgrade' AND status=0;
