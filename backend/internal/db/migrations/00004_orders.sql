-- +goose Up
CREATE SEQUENCE order_number_seq START 1000;

CREATE TABLE addresses (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label         VARCHAR(40) NOT NULL DEFAULT 'Home',
    recipient     VARCHAR(120) NOT NULL,
    phone         VARCHAR(32) NOT NULL,
    address_line1 VARCHAR(200) NOT NULL,
    address_line2 VARCHAR(200),
    city          VARCHAR(80) NOT NULL,
    province      VARCHAR(80) NOT NULL,
    postal_code   VARCHAR(16) NOT NULL,
    country       VARCHAR(64) NOT NULL DEFAULT 'Indonesia',
    is_default    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_addresses_user ON addresses (user_id);

CREATE TABLE carts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users (id) ON DELETE CASCADE,
    session_key VARCHAR(64),
    status      VARCHAR(16) NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'merged', 'checked_out')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id),
    UNIQUE (session_key)
);

CREATE TABLE cart_items (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id    UUID NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    quantity   INT NOT NULL CHECK (quantity > 0 AND quantity <= 99),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cart_id, variant_id)
);

CREATE INDEX idx_cart_items_cart ON cart_items (cart_id);

CREATE TABLE shipping_methods (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        VARCHAR(32) NOT NULL UNIQUE,
    name        VARCHAR(80) NOT NULL,
    base_fee    NUMERIC(14,2) NOT NULL DEFAULT 0,
    per_kg_fee  NUMERIC(14,2) NOT NULL DEFAULT 0,
    min_days    INT NOT NULL DEFAULT 3,
    max_days    INT NOT NULL DEFAULT 7,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE coupons (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code           VARCHAR(40) NOT NULL UNIQUE,
    type           VARCHAR(10) NOT NULL CHECK (type IN ('percent', 'fixed')),
    value          NUMERIC(14,2) NOT NULL CHECK (value > 0),
    min_subtotal   NUMERIC(14,2) NOT NULL DEFAULT 0,
    max_discount   NUMERIC(14,2) CHECK (max_discount > 0),
    usage_limit    INT NOT NULL DEFAULT 0,
    used_count     INT NOT NULL DEFAULT 0,
    per_user_limit INT NOT NULL DEFAULT 1,
    valid_from     TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until    TIMESTAMPTZ,
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE coupon_usages (
    coupon_id UUID NOT NULL REFERENCES coupons (id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    order_id  UUID NOT NULL,
    used_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (coupon_id, user_id, order_id)
);

CREATE TABLE orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_number    VARCHAR(32) NOT NULL UNIQUE,
    buyer_id        UUID NOT NULL REFERENCES users (id),
    seller_id       UUID NOT NULL REFERENCES users (id),
    status          VARCHAR(24) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'paid', 'packed', 'shipped', 'delivered',
                                      'completed', 'cancelled', 'return_requested', 'returned')),
    currency        VARCHAR(3) NOT NULL DEFAULT 'IDR',
    subtotal        NUMERIC(14,2) NOT NULL DEFAULT 0,
    discount_amount NUMERIC(14,2) NOT NULL DEFAULT 0,
    shipping_fee    NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_amount    NUMERIC(14,2) NOT NULL DEFAULT 0,
    payment_status  VARCHAR(16) NOT NULL DEFAULT 'unpaid'
                    CHECK (payment_status IN ('unpaid', 'pending', 'paid', 'refunded', 'partially_refunded')),
    coupon_code     VARCHAR(40),
    shipping_address JSONB NOT NULL,
    shipping_method VARCHAR(80),
    notes           TEXT,
    placed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at         TIMESTAMPTZ,
    shipped_at      TIMESTAMPTZ,
    delivered_at    TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_buyer ON orders (buyer_id, created_at DESC);
CREATE INDEX idx_orders_seller ON orders (seller_id, created_at DESC);
CREATE INDEX idx_orders_status ON orders (status);
CREATE INDEX idx_orders_placed ON orders (placed_at) WHERE status = 'pending';

CREATE TABLE order_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id      UUID NOT NULL,
    variant_id      UUID NOT NULL,
    seller_id       UUID NOT NULL,
    product_name    VARCHAR(200) NOT NULL,
    variant_name    VARCHAR(160) NOT NULL,
    sku             VARCHAR(80) NOT NULL,
    unit_price      NUMERIC(14,2) NOT NULL,
    quantity        INT NOT NULL CHECK (quantity > 0),
    weight_grams    INT NOT NULL DEFAULT 0,
    total           NUMERIC(14,2) NOT NULL,
    image_url       TEXT,
    status          VARCHAR(24) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'paid', 'packed', 'shipped', 'delivered',
                                      'cancelled', 'return_requested', 'returned')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_items_order ON order_items (order_id);
CREATE INDEX idx_order_items_product ON order_items (product_id);

CREATE TABLE order_events (
    id          BIGSERIAL PRIMARY KEY,
    order_id    UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status VARCHAR(24),
    to_status   VARCHAR(24) NOT NULL,
    actor_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_events_order ON order_events (order_id, created_at);

CREATE TABLE inventory_reservations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    order_id   UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    quantity   INT NOT NULL CHECK (quantity > 0),
    status     VARCHAR(16) NOT NULL DEFAULT 'held'
               CHECK (status IN ('held', 'consumed', 'released')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reservations_variant ON inventory_reservations (variant_id, status);
CREATE INDEX idx_reservations_expiry ON inventory_reservations (status, expires_at);

CREATE TABLE stock_ledger (
    id         BIGSERIAL PRIMARY KEY,
    variant_id UUID NOT NULL REFERENCES product_variants (id),
    order_id   UUID,
    change     INT NOT NULL,
    reason     VARCHAR(40) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_stock_ledger_variant ON stock_ledger (variant_id, created_at DESC);

-- +goose Down
DROP SEQUENCE IF EXISTS order_number_seq;
DROP TABLE IF EXISTS stock_ledger;
DROP TABLE IF EXISTS inventory_reservations;
DROP TABLE IF EXISTS order_events;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS coupon_usages;
DROP TABLE IF EXISTS coupons;
DROP TABLE IF EXISTS shipping_methods;
DROP TABLE IF EXISTS cart_items;
DROP TABLE IF EXISTS carts;
DROP TABLE IF EXISTS addresses;
