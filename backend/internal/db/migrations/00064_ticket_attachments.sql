-- +goose Up
-- Ticket message attachments (photos of the problem, like return evidence).
--
-- Reuses the return-claim rule: at most 3 files, each one a file this
-- platform uploaded (validated in Go by NormalizeEvidenceURLs, not by the
-- schema -- the schema just holds them).
ALTER TABLE ticket_messages ADD COLUMN IF NOT EXISTS attachments JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE ticket_messages DROP COLUMN IF EXISTS attachments;
