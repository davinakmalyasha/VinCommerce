-- +goose Up
ALTER TABLE refresh_sessions
    ADD COLUMN IF NOT EXISTS prev_hash VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_refresh_prev_hash ON refresh_sessions (prev_hash)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_refresh_prev_hash;
ALTER TABLE refresh_sessions DROP COLUMN IF EXISTS prev_hash;
