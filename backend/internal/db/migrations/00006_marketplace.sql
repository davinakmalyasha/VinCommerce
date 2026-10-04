-- +goose Up
CREATE TABLE IF NOT EXISTS stores (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    name          VARCHAR(120) NOT NULL,
    slug          VARCHAR(140) NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    logo_url      TEXT,
    banner_url    TEXT,
    status        VARCHAR(16) NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'active', 'suspended', 'rejected')),
    rating        NUMERIC(3,2) NOT NULL DEFAULT 0,
    rating_count  INT NOT NULL DEFAULT 0,
    products_count INT NOT NULL DEFAULT 0,
    joined_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_stores_status ON stores (status);

CREATE TABLE IF NOT EXISTS seller_kyc (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id       UUID NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
    owner_name     VARCHAR(120) NOT NULL,
    id_number      VARCHAR(40) NOT NULL,
    id_document_url TEXT,
    bank_name      VARCHAR(80) NOT NULL,
    bank_account   VARCHAR(40) NOT NULL,
    status         VARCHAR(16) NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'rejected')),
    admin_note     TEXT,
    reviewed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kyc_store ON seller_kyc (store_id);

CREATE TABLE IF NOT EXISTS return_requests (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES orders (id),
    order_item_id UUID NOT NULL REFERENCES order_items (id),
    buyer_id      UUID NOT NULL REFERENCES users (id),
    seller_id     UUID NOT NULL REFERENCES users (id),
    reason        VARCHAR(24) NOT NULL
                  CHECK (reason IN ('wrong_item', 'defective', 'not_as_described', 'other')),
    description   TEXT NOT NULL DEFAULT '',
    evidence_urls JSONB NOT NULL DEFAULT '[]',
    status        VARCHAR(16) NOT NULL DEFAULT 'requested'
                  CHECK (status IN ('requested', 'approved', 'rejected', 'returned', 'refunded', 'closed')),
    resolution    VARCHAR(16) CHECK (resolution IN ('refund', 'replacement', 'none')),
    seller_note   TEXT,
    admin_note    TEXT,
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_returns_buyer ON return_requests (buyer_id, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_returns_seller ON return_requests (seller_id, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_returns_status ON return_requests (status);

-- +goose Down
DROP TABLE IF EXISTS return_requests;
DROP TABLE IF EXISTS seller_kyc;
DROP TABLE IF EXISTS stores;
