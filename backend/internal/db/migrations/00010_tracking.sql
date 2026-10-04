-- +goose Up
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS tracking_number VARCHAR(64),
    ADD COLUMN IF NOT EXISTS carrier VARCHAR(40);

-- +goose Down
ALTER TABLE orders
    DROP COLUMN IF EXISTS tracking_number,
    DROP COLUMN IF EXISTS carrier;
