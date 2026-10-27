-- +goose Up
-- Off-platform payments arrive as a CLAIM and leave as a DECISION.
--
-- The buyer endpoint used to mark the order paid on the buyer's own say-so: a
-- reference string and an amount matching the total were enough, and nothing checked
-- that the money existed. That is a claim, not a receipt.
--
-- A row here is that claim. The order stays `pending` until an admin approves it, and
-- approval runs the same paid-transition (payment intent, stock consumption) the
-- endpoint used to run directly.
CREATE TABLE IF NOT EXISTS external_payment_claims (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    buyer_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reference   VARCHAR(100) NOT NULL,
    amount      NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    paid_at     TIMESTAMPTZ NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by UUID REFERENCES users (id),
    reviewed_at TIMESTAMPTZ,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open claim per order: a second submission while the first is pending is a
-- duplicate, not a new fact.
CREATE UNIQUE INDEX IF NOT EXISTS idx_external_claims_pending_order
    ON external_payment_claims (order_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_external_claims_status
    ON external_payment_claims (status, created_at);

-- +goose Down
DROP TABLE IF EXISTS external_payment_claims;
