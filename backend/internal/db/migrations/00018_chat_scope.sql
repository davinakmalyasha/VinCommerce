-- +goose Up
ALTER TABLE chat_sessions
    ADD COLUMN IF NOT EXISTS order_id UUID REFERENCES orders (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS type VARCHAR(12) NOT NULL DEFAULT 'support' CHECK (type IN ('support', 'seller'));

CREATE INDEX IF NOT EXISTS idx_chat_sessions_order ON chat_sessions (order_id);

-- +goose Down
ALTER TABLE chat_sessions
    DROP COLUMN IF EXISTS order_id,
    DROP COLUMN IF EXISTS type;
-- Removed with the column in Postgres, but stated explicitly so the rollback is
-- self-describing rather than relying on cascade behaviour. Found by
-- scripts/check-migration.mjs once the CI step started checking this file.
DROP INDEX IF EXISTS idx_chat_sessions_order;
