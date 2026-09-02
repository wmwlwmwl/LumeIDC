-- 仅手机号验证模块的数据；实名人工审核使用独立表，不与本表混用。
ALTER TABLE users ADD COLUMN IF NOT EXISTS phone_e164 TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS phone_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS phone_updated_at TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS users_phone_e164_unique ON users(phone_e164) WHERE phone_e164 IS NOT NULL;

CREATE TABLE IF NOT EXISTS phone_verification_challenges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('bind','change')),
    phone_e164 TEXT NOT NULL,
    code_hmac TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    request_ip TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS phone_challenge_active_unique
    ON phone_verification_challenges(user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX IF NOT EXISTS phone_challenge_lookup
    ON phone_verification_challenges(user_id, purpose, created_at DESC);
CREATE INDEX IF NOT EXISTS phone_challenge_rate
    ON phone_verification_challenges(phone_e164, created_at DESC);
CREATE INDEX IF NOT EXISTS phone_challenge_ip_rate
    ON phone_verification_challenges(request_ip, created_at DESC);

-- Deprecated compatibility table retained for old installations. New manual and automatic
-- identity modules use their own tables created by later migrations.
CREATE TABLE IF NOT EXISTS real_name_submissions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    source TEXT NOT NULL DEFAULT 'manual' CHECK (source = 'manual'),
    provider_ref TEXT NOT NULL DEFAULT '',
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
CREATE UNIQUE INDEX IF NOT EXISTS real_name_pending_user_unique
    ON real_name_submissions(user_id) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS real_name_identity_active_unique
    ON real_name_submissions(identity_number_hmac) WHERE status IN ('pending','approved');
CREATE INDEX IF NOT EXISTS real_name_status_submitted
    ON real_name_submissions(status, submitted_at DESC);
CREATE INDEX IF NOT EXISTS real_name_user_history
    ON real_name_submissions(user_id, id DESC);
