-- +goose Up
CREATE TABLE IF NOT EXISTS low_stock_alerts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_id, variant_id)
);

-- +goose Down
DROP TABLE IF EXISTS low_stock_alerts;
