-- +goose Up
-- Admin takedowns are sticky: a moderation-locked product cannot be
-- re-activated by its seller, only by staff.
ALTER TABLE products ADD COLUMN IF NOT EXISTS moderation_locked BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE products DROP COLUMN IF EXISTS moderation_locked;
