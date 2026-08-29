-- (gateway, trade_no) 唯一约束：防止重复回调核销。
-- 已支付账单中若存在重复 (gateway, trade_no)，保留最早 paid_at 的那条，
-- 将其余重复记录的 trade_no 追加后缀使其唯一，避免约束创建失败。
WITH ranked AS (
    SELECT id, gateway, trade_no, paid_at,
           row_number() OVER (PARTITION BY gateway, trade_no ORDER BY paid_at NULLS LAST, id) AS rn
    FROM invoices
    WHERE status = 1 AND gateway != '' AND trade_no != ''
),
dupes AS (
    SELECT id, trade_no, rn FROM ranked WHERE rn > 1
)
UPDATE invoices inv
SET trade_no = dupes.trade_no || '#dup' || dupes.rn
FROM dupes
WHERE inv.id = dupes.id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_gateway_trade
    ON invoices(gateway, trade_no) WHERE gateway != '' AND trade_no != '';
