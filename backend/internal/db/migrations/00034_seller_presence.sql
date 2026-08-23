-- +goose Up
ALTER TABLE stores
    ADD COLUMN IF NOT EXISTS response_rate_pct INT,
    ADD COLUMN IF NOT EXISTS avg_reply_minutes INT;

-- +goose Down
ALTER TABLE stores
    DROP COLUMN IF EXISTS response_rate_pct,
    DROP COLUMN IF EXISTS avg_reply_minutes;
