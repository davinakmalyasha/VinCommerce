-- +goose Up
CREATE TABLE IF NOT EXISTS back_in_stock_alerts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    variant_id   UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    status       VARCHAR(10) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'triggered', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    triggered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_back_in_stock_user ON back_in_stock_alerts (user_id);

-- +goose Down
DROP TABLE IF EXISTS back_in_stock_alerts;
