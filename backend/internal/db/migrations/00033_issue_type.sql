-- +goose Up
ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS issue_type VARCHAR(20) NOT NULL DEFAULT 'return'
        CHECK (issue_type IN ('return', 'item_not_received'));

-- +goose Down
ALTER TABLE return_requests DROP COLUMN IF EXISTS issue_type;
