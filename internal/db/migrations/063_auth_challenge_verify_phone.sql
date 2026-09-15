-- 「验证当前手机」补验流程：auth_challenges.purpose 增加 verify_phone 用途。
-- 注意：新增用途需在既有已应用的 062 约束基础上再次重建（062 仅覆盖换绑用途）。
ALTER TABLE auth_challenges DROP CONSTRAINT IF EXISTS auth_challenges_purpose_check;
ALTER TABLE auth_challenges ADD CONSTRAINT auth_challenges_purpose_check
  CHECK (purpose IN ('register','login','reset_password','bind_phone','change_phone','verify_email','profile_email','profile_email_old','profile_phone_old','verify_phone'));