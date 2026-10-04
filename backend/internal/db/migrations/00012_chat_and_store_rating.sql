-- +goose Up
-- Recompute store rating from its seller's product reviews.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION recalc_product_rating() RETURNS TRIGGER AS $func$
BEGIN
    UPDATE products p
    SET avg_rating = COALESCE(r.avg, 0),
        rating_count = COALESCE(r.cnt, 0)
    FROM (
        SELECT product_id, AVG(rating)::numeric(3,2) AS avg, COUNT(*)::int AS cnt
        FROM product_reviews
        WHERE product_id = COALESCE(NEW.product_id, OLD.product_id) AND status = 'approved'
        GROUP BY product_id
    ) r
    WHERE p.id = r.product_id;

    UPDATE stores s
    SET rating = COALESCE(agg.avg, 0),
        rating_count = COALESCE(agg.cnt, 0)
    FROM (
        SELECT p.seller_id, AVG(p.avg_rating)::numeric(3,2) AS avg, SUM(p.rating_count)::int AS cnt
        FROM products p
        WHERE p.seller_id = (SELECT seller_id FROM products WHERE id = COALESCE(NEW.product_id, OLD.product_id))
        GROUP BY p.seller_id
    ) agg
    WHERE s.owner_id = agg.seller_id;

    RETURN COALESCE(NEW, OLD);
END;
$func$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS chat_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id),
    agent_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    status      VARCHAR(12) NOT NULL DEFAULT 'open'
                CHECK (status IN ('open', 'closed')),
    source      VARCHAR(16) NOT NULL DEFAULT 'widget'
                CHECK (source IN ('widget', 'ai_escalated')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_chat_sessions_user ON chat_sessions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_queue ON chat_sessions (status, created_at) WHERE status = 'open';

CREATE TABLE IF NOT EXISTS chat_messages (
    id          BIGSERIAL PRIMARY KEY,
    session_id  UUID NOT NULL REFERENCES chat_sessions (id) ON DELETE CASCADE,
    sender_role VARCHAR(12) NOT NULL CHECK (sender_role IN ('customer', 'agent', 'system')),
    sender_id   UUID REFERENCES users (id) ON DELETE SET NULL,
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages (session_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS chat_sessions;
