-- +goose Up
-- SLA breach pings must not spam: one ping per ticket per day while it stays
-- overdue. sla_pinged_at records the last ping; the overdue query only
-- returns tickets never pinged or pinged over 24h ago.
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS sla_pinged_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE support_tickets DROP COLUMN IF EXISTS sla_pinged_at;
