ALTER TABLE products ADD COLUMN IF NOT EXISTS profit_type SMALLINT NOT NULL DEFAULT 0;     -- 0百分比 1固定金额
ALTER TABLE products ADD COLUMN IF NOT EXISTS profit_value NUMERIC(12,2) NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS profit NUMERIC(12,2);                            -- 下单时按售价计算并落库
