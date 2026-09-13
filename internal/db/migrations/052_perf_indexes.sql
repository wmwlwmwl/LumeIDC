-- 补充高频过滤字段的索引：
-- 1) orders.status：仪表盘 count(*) 按状态统计、后台订单列表按状态过滤此前均为顺序扫描。
-- 2) payment_attempts.gateway_code / status：支付网关管理页按网关与状态过滤待补单/历史记录。
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_gateway ON payment_attempts(gateway_code);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_status ON payment_attempts(status);
