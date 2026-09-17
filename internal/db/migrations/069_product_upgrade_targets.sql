-- 可升级产品白名单。
-- 成熟系统（魔方财务 shd_product_upgrade_products / 魔方v10 idcsmart_product_upgrade_product）
-- 都用「显式关联表」约束可升级范围，而不是把同服务器的全部产品都列出来任挑。
CREATE TABLE IF NOT EXISTS product_upgrade_targets (
  product_id        BIGINT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  target_product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  PRIMARY KEY (product_id, target_product_id)
);
CREATE INDEX IF NOT EXISTS product_upgrade_targets_target_idx ON product_upgrade_targets(target_product_id);

-- 白名单开关：用于区分「未配置白名单」与「白名单配置为空」。
-- 默认 false —— 未启用时维持旧行为（同服务器其它产品），避免升级部署后
-- 全站服务一夜之间失去升降级入口；管理员逐个产品启用后才强制白名单。
ALTER TABLE products ADD COLUMN IF NOT EXISTS upgrade_whitelist_enabled BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN products.upgrade_whitelist_enabled IS '启用后仅允许升级到白名单内产品（空白名单=隐藏升级入口）';
