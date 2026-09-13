-- 账单余额抵扣：支持“余额 + 在线”组合支付。
-- credit = 本账单已用余额抵扣的部分；在线支付仅针对剩余部分，手续费只对在线部分收取。
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS credit NUMERIC(12,2) NOT NULL DEFAULT 0;

COMMENT ON COLUMN invoices.credit IS '本账单已使用余额抵扣的金额（在线支付只针对 amount-credit，手续费只对在线部分收）';
