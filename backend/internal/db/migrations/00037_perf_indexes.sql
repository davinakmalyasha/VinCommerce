-- +goose Up
-- Performance & integrity hardening: hot-path indexes and live viewer counters.

-- Checkout/cancel/sweeper hit reservations by (order_id, status) inside write
-- transactions that also hold variant row locks; without this index every
-- release/consume is a sequential scan while holding locks.
CREATE INDEX IF NOT EXISTS idx_reservations_order
    ON inventory_reservations (order_id, status);

-- Flash-sale pricing is resolved per cart line at quote/place time.
CREATE INDEX IF NOT EXISTS idx_flash_items_variant
    ON flash_sale_items (variant_id);

-- Seller analytics, digests and top-product queries filter order_items by seller.
CREATE INDEX IF NOT EXISTS idx_order_items_seller
    ON order_items (seller_id, created_at DESC);

-- Price facet filters / sort-by-price scan active variants repeatedly.
CREATE INDEX IF NOT EXISTS idx_variants_price_active
    ON product_variants (price) WHERE is_active;

-- JSONB attribute facets: p.attributes->>'slug' = ANY(...) has no index support.
CREATE INDEX IF NOT EXISTS idx_products_attrs
    ON products USING gin (attributes jsonb_path_ops);

-- Unread notification badge polls user_id + read_at IS NULL.
CREATE INDEX IF NOT EXISTS idx_notif_unread
    ON notifications (user_id) WHERE read_at IS NULL;

-- Worker alert sweeps filter active alerts by variant price/target.
CREATE INDEX IF NOT EXISTS idx_price_alerts_active
    ON price_alerts (variant_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_backinstock_active
    ON back_in_stock_alerts (variant_id) WHERE status = 'active';

-- Refresh-session purge scans expiry; no index existed on expires_at.
CREATE INDEX IF NOT EXISTS idx_refresh_sessions_expires
    ON refresh_sessions (expires_at);

-- A review per purchased item is unique. Partial predicate keeps non-purchase
-- reviews (NULL item) unrestricted — NULLS NOT DISTINCT would wrongly allow
-- only ONE such review per user across all products.
ALTER TABLE product_reviews
    DROP CONSTRAINT IF EXISTS product_reviews_user_id_order_item_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_reviews_user_item_nnd
    ON product_reviews (user_id, order_item_id) WHERE order_item_id IS NOT NULL;

-- True concurrent-viewer high-water mark for live commerce.
ALTER TABLE live_sessions ADD COLUMN IF NOT EXISTS viewer_count INT NOT NULL DEFAULT 0;

-- +goose Down
DROP INDEX IF EXISTS idx_reservations_order;
DROP INDEX IF EXISTS idx_flash_items_variant;
DROP INDEX IF EXISTS idx_order_items_seller;
DROP INDEX IF EXISTS idx_variants_price_active;
DROP INDEX IF EXISTS idx_products_attrs;
DROP INDEX IF EXISTS idx_notif_unread;
DROP INDEX IF EXISTS idx_price_alerts_active;
DROP INDEX IF EXISTS idx_backinstock_active;
DROP INDEX IF EXISTS idx_refresh_sessions_expires;
DROP INDEX IF EXISTS uq_reviews_user_item_nnd;
-- Drop before add. The Up dropped this constraint and replaced it with the
-- partial index above, so a rollback has to drop it again first: a later
-- migration's Down may already have restored it, and
--
--     ALTER TABLE ... ADD CONSTRAINT ...
--
-- then raises `relation "..." already exists` and the rollback stops. Found by
-- rolling the whole chain back newest-first against a scratch database.
ALTER TABLE product_reviews DROP CONSTRAINT IF EXISTS product_reviews_user_id_order_item_id_key;
ALTER TABLE product_reviews ADD CONSTRAINT product_reviews_user_id_order_item_id_key UNIQUE (user_id, order_item_id);
ALTER TABLE live_sessions DROP COLUMN IF EXISTS viewer_count;
