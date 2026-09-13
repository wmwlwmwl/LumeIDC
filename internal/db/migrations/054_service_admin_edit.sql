-- 后台服务编辑：固定续费价覆盖（对齐魔方财务 host 编辑的续费金额）。
-- NULL = 跟随产品价×配置×利润重算；非空 = 续费下单直接采用该周期固定价。
ALTER TABLE services
  ADD COLUMN renew_monthly   NUMERIC(12,2),
  ADD COLUMN renew_quarterly NUMERIC(12,2),
  ADD COLUMN renew_yearly    NUMERIC(12,2);
