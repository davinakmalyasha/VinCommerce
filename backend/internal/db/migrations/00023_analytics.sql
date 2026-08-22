-- +goose Up
CREATE TABLE product_views (
    id         BIGSERIAL PRIMARY KEY,
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    user_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_product_views_product_time ON product_views (product_id, created_at);
CREATE INDEX idx_product_views_time ON product_views (created_at);

-- +goose Down
DROP TABLE IF EXISTS product_views;
