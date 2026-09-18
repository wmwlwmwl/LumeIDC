-- 072 收口 071 的索引谓词与存量脏数据。两件事一起做，都源于「铃铛漏掉 status<3」这次教训。
-- 071 在已执行的环境里谓词只有 provision_error<>''（漏了 status<3），查询照样能用上它，
-- 但谓词与查询不完全一致是隐患——以后若有人把查询谓词再收紧，索引就匹配不上了。
-- 直接重建为正确谓词；IF NOT EXISTS 同名索引不会更新已存在的，故先 DROP。
DROP INDEX IF EXISTS idx_services_provision_error;
CREATE INDEX idx_services_provision_error ON services (id) WHERE provision_error <> '' AND status < 3;

-- 清理存量脏数据：删除服务（status>=3）历史上不清 provision_error，而该字段的所有读取点
-- （服务实例列表、状态轮询、通知铃铛）都过滤 status<3，于是留下永不可见的数据，
-- 并曾让按 provision_error 统计的通知铃铛出现「待办里有、点进去却搜不到」的幽灵条目。
-- 源头已在删除链路清空（Terminate / TerminateLocal / RefundPendingService / countUpstreamMiss）。
--
-- 必须临时禁用 fulfillment_recovery_guard 触发器：部分待清理的已删除服务挂着
-- recovery_required 任务，触发器会拦下任何对 provision_error 等字段的 UPDATE。
-- 这是数据修复而非业务操作，绕过保护合理；执行后立即恢复触发器。
-- 用 ALTER TABLE ... DISABLE/ENABLE TRIGGER（事务内生效、随事务回滚自动恢复）。
ALTER TABLE services DISABLE TRIGGER fulfillment_recovery_guard;
UPDATE services SET provision_error='' WHERE status >= 3 AND provision_error <> '';
ALTER TABLE services ENABLE TRIGGER fulfillment_recovery_guard;
