-- 邮箱/手机号两步换绑验证码：auth_challenges.purpose 增加换绑相关用途。
-- profile_email 为既有「邮箱换绑发新邮箱码」用途（此前 CHECK 漏配，直接使用会报错）；
-- profile_email_old / profile_phone_old 为两步换绑「验证原渠道」新增用途。
ALTER TABLE auth_challenges DROP CONSTRAINT IF EXISTS auth_challenges_purpose_check;
ALTER TABLE auth_challenges ADD CONSTRAINT auth_challenges_purpose_check
  CHECK (purpose IN ('register','login','reset_password','bind_phone','change_phone','verify_email','profile_email','profile_email_old','profile_phone_old'));