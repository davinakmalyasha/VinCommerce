-- +goose Up
CREATE TABLE store_follows (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    store_id   UUID NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, store_id)
);

CREATE INDEX idx_store_follows_store ON store_follows (store_id);

ALTER TABLE stores ADD COLUMN follower_count INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE stores DROP COLUMN IF EXISTS follower_count;
DROP TABLE IF EXISTS store_follows;
