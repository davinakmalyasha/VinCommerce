-- +goose Up
ALTER TABLE orders ADD COLUMN external_payment_ref VARCHAR(128);
ALTER TABLE orders ADD COLUMN external_paid_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE orders DROP COLUMN IF EXISTS external_payment_ref;
ALTER TABLE orders DROP COLUMN IF EXISTS external_paid_at;
