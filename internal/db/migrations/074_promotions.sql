-- 营销活动插件：活动主表、活动-商品关联、限量名额、领券记录、统计

CREATE TABLE IF NOT EXISTS promotions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,                              -- 活动名称
    description TEXT NOT NULL DEFAULT '',            -- 活动简介
    type TEXT NOT NULL CHECK (type IN ('discount','full_reduction','new_user','flash_sale','coupon_giveaway','bogo','group_buy')),
    banner TEXT NOT NULL DEFAULT '',                 -- 活动横幅图 URL
    notice TEXT NOT NULL DEFAULT '',                 -- 滚动公告文字
    rules_text TEXT NOT NULL DEFAULT '',             -- 活动规则说明（弹窗展示）
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    limit_per_user INT NOT NULL DEFAULT 0,           -- 每人限购数量(0=不限)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 活动-商品关联（一个活动可绑定多个商品，每个商品独立配置规则参数）
CREATE TABLE IF NOT EXISTS promotion_products (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    promotion_id BIGINT NOT NULL REFERENCES promotions(id) ON DELETE CASCADE,
    product_id BIGINT NOT NULL REFERENCES products(id),
    priceset_id BIGINT REFERENCES pricesets(id),     -- NULL=全部价格组
    cycle TEXT,                                       -- NULL=全部周期
    rules JSONB NOT NULL DEFAULT '{}'
    -- discount:       {"price": 99.00}
    -- full_reduction: {"threshold": 200, "reduce": 50}
    -- new_user:       {"price": 99.00}
    -- flash_sale:     {"price": 99.00, "stock": 50}
    -- coupon_giveaway:{"coupon_id": 12}
);
CREATE INDEX IF NOT EXISTS idx_promo_products_promo ON promotion_products(promotion_id);
CREATE INDEX IF NOT EXISTS idx_promo_products_product ON promotion_products(product_id);

-- 限量抢购名额（独立于上游库存）
CREATE TABLE IF NOT EXISTS promotion_quota (
    promotion_product_id BIGINT PRIMARY KEY REFERENCES promotion_products(id) ON DELETE CASCADE,
    total INT NOT NULL DEFAULT -1,   -- -1 不限
    sold INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 用户领券记录（防重复领取）
CREATE TABLE IF NOT EXISTS promotion_coupon_claims (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    promotion_id BIGINT NOT NULL REFERENCES promotions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    coupon_id BIGINT NOT NULL REFERENCES coupons(id),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(promotion_id, user_id)
);

-- 活动数据统计
CREATE TABLE IF NOT EXISTS promotion_stats (
    promotion_id BIGINT PRIMARY KEY REFERENCES promotions(id) ON DELETE CASCADE,
    views INT NOT NULL DEFAULT 0,              -- 访问量
    claimed INT NOT NULL DEFAULT 0,            -- 领券数
    orders INT NOT NULL DEFAULT 0,             -- 下单量
    paid_amount NUMERIC(12,2) NOT NULL DEFAULT 0  -- 成交金额
);

-- orders 表记录活动来源
ALTER TABLE orders ADD COLUMN IF NOT EXISTS promotion_id BIGINT REFERENCES promotions(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS promo_product_id BIGINT REFERENCES promotion_products(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS promo_type TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS promo_discount NUMERIC(12,2) NOT NULL DEFAULT 0;
