-- +goose Up
-- Browsing history reads product_views per user, newest first. Without an
-- index on (user_id, created_at) every history open scans the whole events
-- table; the existing indexes start with product_id or created_at.
CREATE INDEX IF NOT EXISTS idx_product_views_user_time
    ON product_views (user_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_product_views_user_time;
