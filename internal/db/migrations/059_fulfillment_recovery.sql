-- 领取版本不随 attempts 归零，避免等待外部条件后旧执行者再次获得写入权。
ALTER TABLE fulfillment_jobs ADD COLUMN claim_version BIGINT NOT NULL DEFAULT 0;
-- 未知上游结果与涨价/等待充值的人工暂停分开：普通重试不得解除此隔离。
ALTER TABLE fulfillment_jobs ADD COLUMN recovery_required BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_fulfillment_jobs_recovery ON fulfillment_jobs(service_id) WHERE recovery_required;

-- 隔离后统一保护履约证据与状态，覆盖检查点仓库、迟到线程和后台直接回写。
-- 服务 UPDATE 自带行锁，与 Claim/人工入队的服务锁串行；恢复先写提示再隔离任务，同事务提交。
CREATE FUNCTION guard_fulfillment_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.status, NEW.upstream_host_id, NEW.provision_data, NEW.provision_error,
           NEW.transition_state, NEW.desired_status, NEW.product_id, NEW.expires_at, NEW.password_crypt,
           NEW.user_id, NEW.order_id, NEW.server_id, NEW.upstream_provider, NEW.upstream_pid, NEW.cycle, NEW.config_snapshot)
       IS DISTINCT FROM
       ROW(OLD.status, OLD.upstream_host_id, OLD.provision_data, OLD.provision_error,
           OLD.transition_state, OLD.desired_status, OLD.product_id, OLD.expires_at, OLD.password_crypt,
           OLD.user_id, OLD.order_id, OLD.server_id, OLD.upstream_provider, OLD.upstream_pid, OLD.cycle, OLD.config_snapshot)
       AND EXISTS (SELECT 1 FROM fulfillment_jobs WHERE service_id=OLD.id AND recovery_required)
    THEN
        RAISE EXCEPTION '履约任务上游结果未知，请先核对账单和实例，禁止覆盖服务状态或检查点';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER fulfillment_recovery_guard BEFORE UPDATE ON services
FOR EACH ROW EXECUTE FUNCTION guard_fulfillment_recovery();
