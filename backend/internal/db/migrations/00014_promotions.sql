-- +goose Up
ALTER TABLE coupons ADD COLUMN seller_id UUID REFERENCES users (id) ON DELETE CASCADE;
CREATE INDEX idx_coupons_seller ON coupons (seller_id) WHERE seller_id IS NOT NULL;

CREATE TABLE price_alerts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    variant_id   UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    target_price NUMERIC(14,2) NOT NULL,
    status       VARCHAR(10) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'triggered', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_price_alerts_user ON price_alerts (user_id);

CREATE TABLE bundles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id  UUID NOT NULL REFERENCES users (id),
    name       VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price      NUMERIC(14,2) NOT NULL CHECK (price >= 0),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE bundle_items (
    bundle_id  UUID NOT NULL REFERENCES bundles (id) ON DELETE CASCADE,
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    quantity   INT NOT NULL DEFAULT 1 CHECK (quantity > 0),
    PRIMARY KEY (bundle_id, variant_id)
);

ALTER TABLE stores ADD COLUMN free_shipping_threshold NUMERIC(14,2);

-- +goose Down
ALTER TABLE stores DROP COLUMN IF EXISTS free_shipping_threshold;
DROP TABLE IF EXISTS bundle_items;
DROP TABLE IF EXISTS bundles;
DROP TABLE IF EXISTS price_alerts;
ALTER TABLE coupons DROP COLUMN IF EXISTS seller_id;
