-- 优惠码场景化：适用范围（新购/续费）、循环期数、需求商品（参照魔方 v10 promo_code 插件）。
--
-- apply_scope:      new=仅新购（默认，历史行为）| renew=仅续费 | both=均可。
-- recurring:        同一用户可用总次数 = 1 + recurring（0=每人一次；N=新购 1 次 + 续费再输码 N 次）。
-- need_product_ids: 非空时要求用户持有 status=1（激活）的指定产品服务，否则拒绝用券。

ALTER TABLE coupons ADD COLUMN IF NOT EXISTS apply_scope TEXT NOT NULL DEFAULT 'new';
ALTER TABLE coupons ADD CONSTRAINT coupons_apply_scope_check CHECK (apply_scope IN ('new','renew','both'));
ALTER TABLE coupons ADD COLUMN IF NOT EXISTS recurring INT NOT NULL DEFAULT 0;
ALTER TABLE coupons ADD COLUMN IF NOT EXISTS need_product_ids JSONB NOT NULL DEFAULT '[]';
