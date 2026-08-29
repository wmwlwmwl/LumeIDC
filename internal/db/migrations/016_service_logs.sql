-- 服务操作日志：记录用户在本面板对实例执行的操作（对齐 ZJMF-CBAP 本地 SystemLog 做法，非上游数据）。
CREATE TABLE IF NOT EXISTS service_logs (
    id BIGSERIAL PRIMARY KEY,
    service_id BIGINT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_service_logs_service ON service_logs(service_id, id DESC);
