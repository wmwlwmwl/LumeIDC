-- 补齐早期安装缺失的默认价格组；已有价格组时保持原数据不变。
INSERT INTO pricesets (name)
SELECT '默认价格组'
WHERE NOT EXISTS (SELECT 1 FROM pricesets);
