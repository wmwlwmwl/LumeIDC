-- 手机验证码挑战增加登录用途；不修改已执行的 029 迁移。
ALTER TABLE phone_verification_challenges DROP CONSTRAINT IF EXISTS phone_verification_challenges_purpose_check;
ALTER TABLE phone_verification_challenges ADD CONSTRAINT phone_verification_challenges_purpose_check CHECK (purpose IN ('bind','change','login'));
