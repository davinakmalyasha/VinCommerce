-- +goose Up
CREATE TABLE product_qa (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users (id),
    question     TEXT NOT NULL,
    answer       TEXT,
    answered_by  UUID REFERENCES users (id) ON DELETE SET NULL,
    answered_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qa_product ON product_qa (product_id, created_at DESC);

CREATE TABLE review_replies (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id  UUID NOT NULL REFERENCES product_reviews (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id),
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (review_id)
);

-- +goose Down
DROP TABLE IF EXISTS review_replies;
DROP TABLE IF EXISTS product_qa;
