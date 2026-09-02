-- 将人工实名与自动实名彻底分成独立模块/状态表；旧 real_name_submissions 仅作兼容来源。
CREATE TABLE IF NOT EXISTS manual_identity_submissions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    legal_name_ciphertext TEXT NOT NULL,
    identity_number_ciphertext TEXT NOT NULL,
    identity_number_hmac TEXT NOT NULL,
    front_photo_ref TEXT NOT NULL,
    back_photo_ref TEXT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    reviewed_by BIGINT REFERENCES admin_users(id),
    rejection_reason TEXT NOT NULL DEFAULT '',
    version INT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS manual_identity_pending_user_unique
    ON manual_identity_submissions(user_id) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS manual_identity_hmac_active_unique
    ON manual_identity_submissions(identity_number_hmac) WHERE status IN ('pending','approved');
CREATE INDEX IF NOT EXISTS manual_identity_status_submitted
    ON manual_identity_submissions(status, submitted_at DESC);
CREATE INDEX IF NOT EXISTS manual_identity_user_history
    ON manual_identity_submissions(user_id, id DESC);

-- 将上一版尚未迁移的人工申请复制到独立人工表；重复执行安全。
INSERT INTO manual_identity_submissions(
    user_id,status,legal_name_ciphertext,identity_number_ciphertext,identity_number_hmac,
    front_photo_ref,back_photo_ref,submitted_at,reviewed_at,reviewed_by,rejection_reason,version,created_at,updated_at
)
SELECT old.user_id,old.status,old.legal_name_ciphertext,old.identity_number_ciphertext,old.identity_number_hmac,
       old.front_photo_ref,old.back_photo_ref,old.submitted_at,old.reviewed_at,old.reviewed_by,
       old.rejection_reason,old.version,old.created_at,old.updated_at
FROM real_name_submissions old
WHERE old.source='manual'
  AND NOT EXISTS (
      SELECT 1 FROM manual_identity_submissions current
      WHERE current.user_id=old.user_id
        AND current.identity_number_hmac=old.identity_number_hmac
  );

-- 自动实名尝试独立存储，不允许人工审核 handler 处理此表。
CREATE TABLE IF NOT EXISTS automatic_identity_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_key TEXT NOT NULL,
    provider_ref TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'initiated' CHECK (status IN ('initiated','pending','approved','rejected','failed','expired')),
    legal_name_ciphertext TEXT NOT NULL,
    identity_number_ciphertext TEXT NOT NULL,
    identity_number_hmac TEXT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    failure_message TEXT NOT NULL DEFAULT '',
    version INT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(provider_key, provider_ref)
);
CREATE UNIQUE INDEX IF NOT EXISTS automatic_identity_pending_user_unique
    ON automatic_identity_attempts(user_id) WHERE status IN ('initiated','pending');
CREATE INDEX IF NOT EXISTS automatic_identity_user_status
    ON automatic_identity_attempts(user_id, status, id DESC);

-- 人工审核独立开关；默认开启并要求已验证手机号，保持既有行为。
INSERT INTO settings(key,value) VALUES
 ('manual_identity_enabled','1'),
 ('manual_identity_requires_verified_phone','1'),
 ('registration_email_enabled','1'),
 ('registration_phone_enabled','0'),
 ('registration_email_verification_required','0'),
 ('registration_phone_verification_required','1'),
 ('login_email_enabled','1'),
 ('login_phone_enabled','1'),
 ('login_phone_otp_enabled','0'),
 ('captcha_enabled','0'),
 ('captcha_register_enabled','0'),
 ('captcha_login_enabled','0'),
 ('captcha_admin_login_enabled','0'),
 ('captcha_email_code_enabled','0'),
 ('captcha_phone_code_enabled','0'),
 ('captcha_password_reset_enabled','0')
ON CONFLICT(key) DO NOTHING;
