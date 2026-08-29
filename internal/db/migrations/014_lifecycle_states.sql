-- 生命周期状态机：desired_status + transition_state。
-- status 0/1/2/3 保持 UI 兼容投影；desired_status 记录期望目标状态；
-- transition_state 记录当前过渡状态（空=稳定，suspending/unsuspending/terminating=过渡中）。
ALTER TABLE services ADD COLUMN IF NOT EXISTS desired_status SMALLINT;
ALTER TABLE services ADD COLUMN IF NOT EXISTS transition_state TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN services.desired_status IS '期望目标状态（NULL=无待处理变更）';
COMMENT ON COLUMN services.transition_state IS '过渡状态：空=稳定，suspending/unsuspending/terminating=过渡中';
