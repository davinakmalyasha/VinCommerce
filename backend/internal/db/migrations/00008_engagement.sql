-- +goose Up
CREATE TABLE wishlists (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, variant_id)
);

CREATE INDEX idx_wishlists_user ON wishlists (user_id, created_at DESC);

CREATE TABLE flash_sales (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);

CREATE TABLE flash_sale_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flash_sale_id UUID NOT NULL REFERENCES flash_sales (id) ON DELETE CASCADE,
    variant_id    UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    sale_price    NUMERIC(14,2) NOT NULL CHECK (sale_price >= 0),
    initial_stock INT NOT NULL DEFAULT 0,
    sold_count    INT NOT NULL DEFAULT 0,
    UNIQUE (flash_sale_id, variant_id)
);

CREATE INDEX idx_flash_items_sale ON flash_sale_items (flash_sale_id);

-- +goose Down
DROP TABLE IF EXISTS flash_sale_items;
DROP TABLE IF EXISTS flash_sales;
DROP TABLE IF EXISTS wishlists;
