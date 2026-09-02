-- 本地图形验证码模块，独立于短信/邮箱 OTP。
CREATE TABLE IF NOT EXISTS captcha_challenges (
    id TEXT PRIMARY KEY,
    scene TEXT NOT NULL,
    answer_hmac TEXT NOT NULL,
    request_ip TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS captcha_challenge_expiry ON captcha_challenges(expires_at);
CREATE INDEX IF NOT EXISTS captcha_challenge_rate ON captcha_challenges(scene, request_ip, created_at DESC);

-- 账号注册/登录渠道和验证策略，服务端按键强制执行。
INSERT INTO settings(key,value) VALUES
 ('registration_email_enabled','1'), ('registration_phone_enabled','0'),
 ('registration_email_verification_required','0'), ('registration_phone_verification_required','1'),
 ('login_email_enabled','1'), ('login_phone_enabled','1'), ('login_phone_otp_enabled','0'),
 ('manual_identity_enabled','1'), ('manual_identity_requires_verified_phone','1'),
 ('captcha_enabled','0'), ('captcha_register_enabled','0'), ('captcha_login_enabled','0'),
 ('captcha_admin_login_enabled','0'), ('captcha_email_code_enabled','0'), ('captcha_phone_code_enabled','0'),
 ('captcha_password_reset_enabled','0')
ON CONFLICT(key) DO NOTHING;

-- 手机号独立注册需要允许邮箱为空；约束至少一个账号标识由应用层和手机号验证流程维护。
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_login_identifier_check CHECK (NULLIF(email,'') IS NOT NULL OR phone_e164 IS NOT NULL);
