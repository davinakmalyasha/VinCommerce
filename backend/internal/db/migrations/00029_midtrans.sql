-- +goose Up
ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS snap_token    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS gateway_txn_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS redirect_url  TEXT;

CREATE INDEX IF NOT EXISTS idx_payment_intents_gateway_ref ON payment_intents (gateway, gateway_ref);

-- +goose Down
DROP INDEX IF EXISTS idx_payment_intents_gateway_ref;
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS snap_token,
    DROP COLUMN IF EXISTS gateway_txn_id,
    DROP COLUMN IF EXISTS redirect_url;
