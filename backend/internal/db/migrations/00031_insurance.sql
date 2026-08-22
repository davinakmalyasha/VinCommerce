-- +goose Up
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS insurance_fee NUMERIC(14,2) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE orders DROP COLUMN IF EXISTS insurance_fee;
