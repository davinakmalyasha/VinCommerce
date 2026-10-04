-- +goose Up
CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at    TIMESTAMPTZ,
    PRIMARY KEY (user_id, code_hash)
);

-- +goose Down
DROP TABLE IF EXISTS two_factor_backup_codes;
