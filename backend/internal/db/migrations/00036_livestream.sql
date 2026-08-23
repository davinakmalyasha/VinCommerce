-- +goose Up
CREATE TABLE live_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id        UUID NOT NULL REFERENCES users (id),
    title            VARCHAR(160) NOT NULL,
    thumbnail_url    TEXT,
    youtube_video_id VARCHAR(32),
    status           VARCHAR(12) NOT NULL DEFAULT 'scheduled'
                     CHECK (status IN ('scheduled', 'live', 'ended')),
    viewer_peak      INT NOT NULL DEFAULT 0,
    scheduled_at     TIMESTAMPTZ,
    started_at       TIMESTAMPTZ,
    ended_at         TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_live_sessions_status ON live_sessions (status, created_at DESC);

CREATE TABLE live_products (
    session_id    UUID NOT NULL REFERENCES live_sessions (id) ON DELETE CASCADE,
    variant_id    UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    price_override NUMERIC(14,2) CHECK (price_override IS NULL OR price_override > 0),
    pinned_at     TIMESTAMPTZ,
    unpinned_at   TIMESTAMPTZ,
    PRIMARY KEY (session_id, variant_id)
);

-- +goose Down
DROP TABLE IF EXISTS live_products;
DROP TABLE IF EXISTS live_sessions;
