-- +goose Up
-- Helpful votes on knowledge-base articles (Amazon-style "was this helpful?").
--
-- Mirrors review_helpful: one row per (article, user), counter on the article
-- kept in the same transaction so the count and the votes cannot disagree.
ALTER TABLE help_articles ADD COLUMN IF NOT EXISTS helpful_count INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS help_article_helpful (
    article_id UUID NOT NULL REFERENCES help_articles (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    helpful    BOOLEAN NOT NULL,
    PRIMARY KEY (article_id, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS help_article_helpful;
ALTER TABLE help_articles DROP COLUMN IF EXISTS helpful_count;
