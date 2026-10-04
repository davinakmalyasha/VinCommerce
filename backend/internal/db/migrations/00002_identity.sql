-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             CITEXT UNIQUE NOT NULL,
    phone             VARCHAR(32) UNIQUE,
    password_hash     TEXT NOT NULL,
    full_name         VARCHAR(120) NOT NULL,
    roles             TEXT[] NOT NULL DEFAULT '{buyer}',
    status            VARCHAR(16) NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'disabled', 'suspended')),
    email_verified_at TIMESTAMPTZ,
    two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users (lower(email));
CREATE INDEX IF NOT EXISTS idx_users_status ON users (status);

CREATE TABLE IF NOT EXISTS refresh_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_hash  CHAR(64) NOT NULL,
    family_id     UUID NOT NULL,
    device_name   VARCHAR(120),
    ip_address    INET,
    user_agent    TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    last_used_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_refresh_sessions_user ON refresh_sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_sessions_hash ON refresh_sessions (refresh_hash);
CREATE INDEX IF NOT EXISTS idx_refresh_sessions_family ON refresh_sessions (family_id);

CREATE TABLE IF NOT EXISTS email_tokens (
    token_hash CHAR(64) PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    VARCHAR(24) NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_email_tokens_user ON email_tokens (user_id, purpose);

CREATE TABLE IF NOT EXISTS totp_secrets (
    user_id       UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret        TEXT NOT NULL,
    confirmed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    action      VARCHAR(64) NOT NULL,
    entity_type VARCHAR(64),
    entity_id   VARCHAR(64),
    ip_address  INET,
    user_agent  TEXT,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log (entity_type, entity_id);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS totp_secrets;
DROP TABLE IF EXISTS email_tokens;
DROP TABLE IF EXISTS refresh_sessions;
DROP TABLE IF EXISTS users;
