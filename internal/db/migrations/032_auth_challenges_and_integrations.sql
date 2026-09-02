-- 认证挑战与可配置集成 provider 基础表。
CREATE TABLE IF NOT EXISTS auth_challenges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    channel TEXT NOT NULL CHECK (channel IN ('email','phone')),
    purpose TEXT NOT NULL CHECK (purpose IN ('register','login','reset_password','bind_phone','change_phone','verify_email')),
    destination TEXT NOT NULL,
    destination_hmac TEXT NOT NULL,
    code_hmac TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    request_ip TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS auth_challenge_active_unique
    ON auth_challenges(coalesce(user_id, 0), channel, purpose, destination_hmac)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX IF NOT EXISTS auth_challenge_rate_idx
    ON auth_challenges(channel, destination_hmac, created_at DESC);
CREATE INDEX IF NOT EXISTS auth_challenge_ip_rate_idx
    ON auth_challenges(request_ip, created_at DESC);

CREATE TABLE IF NOT EXISTS integration_plugins (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    domain TEXT NOT NULL CHECK (domain IN ('sms','captcha','verification','mail')),
    plugin_key TEXT NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '',
    capabilities JSONB NOT NULL DEFAULT '[]',
    config_json JSONB NOT NULL DEFAULT '{}',
    secret_json BYTEA NOT NULL DEFAULT ''::bytea,
    enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(domain, plugin_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS integration_plugins_one_enabled
    ON integration_plugins(domain) WHERE enabled;
CREATE TABLE IF NOT EXISTS integration_bindings (
    domain TEXT PRIMARY KEY CHECK (domain IN ('sms','captcha','verification','mail')),
    plugin_id BIGINT NOT NULL REFERENCES integration_plugins(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
