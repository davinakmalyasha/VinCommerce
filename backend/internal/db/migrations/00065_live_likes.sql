-- +goose Up
-- Live session likes (Shopee-style hearts).
--
-- One row per (session, user); the counter on the session is kept in the
-- same transaction so count and votes cannot disagree. Unlike toggles the
-- row off so tapping twice is an unlike, not two likes.
ALTER TABLE live_sessions ADD COLUMN IF NOT EXISTS like_count INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS live_likes (
    session_id UUID NOT NULL REFERENCES live_sessions (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS live_likes;
ALTER TABLE live_sessions DROP COLUMN IF EXISTS like_count;
