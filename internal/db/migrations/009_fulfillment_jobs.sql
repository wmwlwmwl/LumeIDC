-- 持久化履约任务与服务级上游快照。
ALTER TABLE services ADD COLUMN IF NOT EXISTS upstream_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN IF NOT EXISTS upstream_pid BIGINT NOT NULL DEFAULT 0;

-- 历史服务回填策略：
-- 1. 无上游 host 的本地服务（upstream_host_id=0）：从产品绑定安全回填 server_id/upstream_provider/upstream_pid
-- 2. 已有上游 host 的服务：保留为空（upstream_pid=0），需人工确认后补填，不能静默宣称历史真值
-- 回填后，新订单在支付事务中写入完整快照；产品改绑不影响已有服务的上游身份。
UPDATE services sv
SET server_id=p.server_id,
    upstream_provider=coalesce(s.provider,''),
    upstream_pid=p.upstream_pid
FROM products p LEFT JOIN servers s ON s.id=p.server_id
WHERE sv.product_id=p.id AND sv.upstream_host_id=0 AND sv.upstream_pid=0;

CREATE TABLE IF NOT EXISTS fulfillment_jobs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id BIGINT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    order_id BIGINT REFERENCES orders(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('provision','renew')),
    cycle TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','retry','succeeded','dead')),
    dedupe_key TEXT NOT NULL UNIQUE,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_fulfillment_jobs_claim
    ON fulfillment_jobs(status, next_attempt_at, lease_until, id);
