-- 分类补充字段 + 产品库存语义对齐
ALTER TABLE product_types ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
