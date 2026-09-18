-- 后台通知铃铛每 60s 统计「待处理的履约失败服务」，失败服务通常是个位数：
-- 用部分索引把扫描范围收敛到这几行，避免每次轮询全表扫 services。
-- 谓词必须与 admin_notifications.go 的查询逐字一致（provision_error<>'' AND status<3），
-- 优化器才会用上这个索引；查询里包 coalesce、或谓词里漏掉 status，都会让它失效。
-- provision_error 为 NOT NULL DEFAULT ''，成功/退款后会被置回空串。
-- status<3 与「服务实例」列表页的过滤一致：删除服务不会清空 provision_error，
-- 不排除已删除的服务会出现「待办里有、点进去却搜不到」的幽灵条目。
CREATE INDEX IF NOT EXISTS idx_services_provision_error ON services (id) WHERE provision_error <> '' AND status < 3;
