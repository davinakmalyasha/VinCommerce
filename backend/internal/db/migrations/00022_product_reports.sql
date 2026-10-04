-- +goose Up
CREATE TABLE IF NOT EXISTS product_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason      VARCHAR(60) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      VARCHAR(10) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    admin_note  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_product_reports_status ON product_reports (status, created_at);

-- +goose Down
DROP TABLE IF EXISTS product_reports;
