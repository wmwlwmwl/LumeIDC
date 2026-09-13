-- 用户「停用申请」：用户在服务详情页提交停用/取消申请，后台审核处理后执行删除。
-- 对齐魔方财务 cancel_requests：type=Immediate(立即)/Endofbilling(等待账单周期结束)。
-- LumeIDC 差异化：管理员处理时可选「本地删除」（仅本地 status=3，不动上游实例）
-- 或「连上游删除」（先调 provider.Terminate 再置本地）。
CREATE TABLE IF NOT EXISTS service_cancel_requests (
    id            BIGSERIAL PRIMARY KEY,
    service_id    BIGINT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL,
    type          TEXT NOT NULL DEFAULT 'Immediate',
    reason        TEXT NOT NULL DEFAULT '',
    reason_detail TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',   -- pending/approved/rejected/withdrawn
    handle_mode   TEXT NOT NULL DEFAULT '',          -- local/upstream（审核通过时）
    handle_note   TEXT NOT NULL DEFAULT '',
    handled_by    BIGINT,
    handled_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 同一服务同时只允许一条待处理申请（部分唯一索引）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_cancel_requests_pending
    ON service_cancel_requests(service_id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_service_cancel_requests_status
    ON service_cancel_requests(status, id DESC);

CREATE INDEX IF NOT EXISTS idx_service_cancel_requests_user
    ON service_cancel_requests(user_id, id DESC);
