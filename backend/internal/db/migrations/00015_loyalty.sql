-- +goose Up
CREATE TABLE loyalty_ledger (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    change     INT NOT NULL,
    reason     VARCHAR(24) NOT NULL,
    ref_id     VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_loyalty_user ON loyalty_ledger (user_id, created_at DESC);

CREATE TABLE referral_codes (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    code       VARCHAR(24) NOT NULL UNIQUE,
    used_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS referral_codes;
DROP TABLE IF EXISTS loyalty_ledger;
