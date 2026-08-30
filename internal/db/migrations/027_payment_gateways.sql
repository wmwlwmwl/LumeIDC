ALTER TABLE gateways ADD COLUMN IF NOT EXISTS driver TEXT NOT NULL DEFAULT 'epay';
ALTER TABLE gateways ADD COLUMN IF NOT EXISTS sort INT NOT NULL DEFAULT 0;

UPDATE gateways SET driver='epay' WHERE driver='';
UPDATE gateways SET sort=id WHERE sort=0;

CREATE TABLE IF NOT EXISTS payment_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    gateway_code TEXT NOT NULL,
    amount NUMERIC(12,2) NOT NULL,
    status SMALLINT NOT NULL DEFAULT 0, -- 0 pending, 1 paid, 2 failed
    merchant_trade_no TEXT NOT NULL DEFAULT '',
    provider_trade_no TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_invoice ON payment_attempts(invoice_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_attempts_provider_trade
    ON payment_attempts(gateway_code, provider_trade_no)
    WHERE provider_trade_no <> '';
