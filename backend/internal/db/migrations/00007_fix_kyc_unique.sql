-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_kyc_store_unique ON seller_kyc (store_id);

-- +goose Down
DROP INDEX IF EXISTS idx_kyc_store_unique;
