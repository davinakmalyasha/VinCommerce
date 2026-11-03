-- +goose Up
-- Ticket SLA deadline (when staff owes the next response).
--
-- Set once at creation from the priority; the policy lives in Go
-- (slaDueFor) so it is unit-tested by value. NULL only for tickets
-- predating this migration.
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS sla_due_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_tickets_sla_due
    ON support_tickets (sla_due_at) WHERE status NOT IN ('resolved', 'closed');

-- +goose Down
DROP INDEX IF EXISTS idx_tickets_sla_due;
ALTER TABLE support_tickets DROP COLUMN IF EXISTS sla_due_at;
