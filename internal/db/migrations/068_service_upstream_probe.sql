-- 上游同步观测：记录同一服务连续「上游不可见」的次数与起始时间。
-- 用途：上游已删除实例但 /host/header 只回业务错误码（不给 domainstatus=Deleted）时，
-- 本地靠累计次数兜底判定资源已消失，避免服务长期挂在 status 1/2（幽灵服务：
-- 机器在上游已销毁，本地仍显示可续费）。查询成功即清零。
ALTER TABLE services ADD COLUMN IF NOT EXISTS upstream_miss_count SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN IF NOT EXISTS upstream_miss_since TIMESTAMPTZ;

COMMENT ON COLUMN services.upstream_miss_count IS '上游不可见连续次数（查询成功即清零）';
COMMENT ON COLUMN services.upstream_miss_since IS '首次判定为上游不可见的时间';
