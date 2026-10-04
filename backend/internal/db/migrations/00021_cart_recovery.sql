-- +goose Up
ALTER TABLE carts ADD COLUMN recovery_sent_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_carts_abandoned ON carts (status, updated_at) WHERE user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_carts_abandoned;
ALTER TABLE carts DROP COLUMN IF EXISTS recovery_sent_at;
