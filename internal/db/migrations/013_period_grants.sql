-- 服务周期授权记录：防止同一账单重复续费。
-- 每张已支付账单只能为服务延长一次周期。
CREATE TABLE IF NOT EXISTS service_period_grants (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id BIGINT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    cycle TEXT NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(invoice_id)
);

CREATE INDEX IF NOT EXISTS idx_period_grants_service ON service_period_grants(service_id);
