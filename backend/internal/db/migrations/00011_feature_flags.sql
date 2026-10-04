-- +goose Up
CREATE TABLE IF NOT EXISTS feature_flags (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key         VARCHAR(64) NOT NULL UNIQUE,
    description VARCHAR(200) NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS feature_flags;
