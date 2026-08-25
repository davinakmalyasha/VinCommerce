-- +goose Up
-- Token versioning: bumped on role/status changes so outstanding access
-- tokens (15-min TTL) are invalidated immediately instead of lingering.
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_ver INT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS token_ver;
