-- 用户余额 + 交易流水
ALTER TABLE users ADD COLUMN IF NOT EXISTS balance NUMERIC(12,2) NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS status SMALLINT NOT NULL DEFAULT 1; -- 已存在则忽略报错可接受

CREATE TABLE IF NOT EXISTS balance_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    amount NUMERIC(12,2) NOT NULL,          -- 正=充值/收入，负=扣减/消费
    balance_after NUMERIC(12,2) NOT NULL,
    type TEXT NOT NULL,                      -- recharge/consume/admin/refund
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_balance_logs_user ON balance_logs(user_id);
