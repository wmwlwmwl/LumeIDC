-- 后台服务编辑：手工配置说明覆盖。
-- NULL = 按产品配置项 + 下单快照自动生成；非空 = 列表/详情直接展示该文本。
-- 用于手工上架或迁移过来的服务（其配置无法由产品配置项推导）。
ALTER TABLE services ADD COLUMN config_desc TEXT;
