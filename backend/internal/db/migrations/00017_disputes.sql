-- +goose Up
CREATE TABLE IF NOT EXISTS disputes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id     UUID NOT NULL REFERENCES orders (id),
    return_id    UUID REFERENCES return_requests (id) ON DELETE SET NULL,
    user_id      UUID NOT NULL REFERENCES users (id),
    seller_id    UUID NOT NULL REFERENCES users (id),
    subject      VARCHAR(200) NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    status       VARCHAR(16) NOT NULL DEFAULT 'open'
                 CHECK (status IN ('open', 'under_review', 'resolved', 'closed')),
    decision     VARCHAR(16) CHECK (decision IN ('buyer', 'seller', 'split', 'none')),
    admin_note   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_disputes_status ON disputes (status);
CREATE INDEX IF NOT EXISTS idx_disputes_user ON disputes (user_id);

CREATE TABLE IF NOT EXISTS dispute_messages (
    id         BIGSERIAL PRIMARY KEY,
    dispute_id UUID NOT NULL REFERENCES disputes (id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users (id),
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dispute_messages ON dispute_messages (dispute_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS dispute_messages;
DROP TABLE IF EXISTS disputes;
