-- 041 补齐 servers 利润列：产品未设利润时回退服务器默认利润（0百分比 1固定金额）。
-- 早期安装脚本有此列，迁移链引入后遗漏，导致全新安装三个后台列表查询报错。
ALTER TABLE servers ADD COLUMN IF NOT EXISTS profit_type SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS profit_value NUMERIC(12,2) NOT NULL DEFAULT 0;