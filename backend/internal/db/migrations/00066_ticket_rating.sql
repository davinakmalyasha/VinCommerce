-- +goose Up
-- Ticket satisfaction rating (Amazon-style "how did we do?").
--
-- Nullable: unrated tickets stay NULL rather than inventing a default that
-- would read as a 0-star review. Rating is only meaningful once staff had a
-- chance to respond, enforced in Go (resolved/closed only), not by the
-- schema -- the schema just holds the score.
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS rating SMALLINT CHECK (rating BETWEEN 1 AND 5);
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS rated_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE support_tickets DROP COLUMN IF EXISTS rated_at;
ALTER TABLE support_tickets DROP COLUMN IF EXISTS rating;
