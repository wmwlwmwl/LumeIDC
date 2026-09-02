-- 产品级实名购买策略与人工审核手机号开关。
-- 已有产品默认保持历史行为（需要实名）；新建产品由仓储显式写入 false。
ALTER TABLE products ADD COLUMN IF NOT EXISTS requires_identity BOOLEAN NOT NULL DEFAULT true;

-- 人工审核是否必须先验证手机号：1 保持旧默认，0 允许未绑定手机号提交。
INSERT INTO settings(key, value) VALUES ('manual_requires_phone', '1')
ON CONFLICT (key) DO NOTHING;

-- 允许内置或受信任的实名 provider 写入 source；source 仍必须非空。
ALTER TABLE real_name_submissions DROP CONSTRAINT IF EXISTS real_name_submissions_source_check;
ALTER TABLE real_name_submissions ADD CONSTRAINT real_name_submissions_source_check CHECK (source <> '');

-- 订单保存创建时的实名要求快照（032 已创建则本语句幂等）。
ALTER TABLE orders ADD COLUMN IF NOT EXISTS identity_required BOOLEAN NOT NULL DEFAULT false;
