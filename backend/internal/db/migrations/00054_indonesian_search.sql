-- +goose Up
-- Search was stemmed with the ENGLISH configuration over Indonesian product
-- names and help articles. English stemming does not strip Indonesian
-- affixes, so "makanan" and "makan" are two unrelated lexemes and a shopper
-- searching the root word finds nothing. PostgreSQL ships an `indonesian`
-- Snowball configuration; this migration switches both search surfaces to it.
--
-- The products column is GENERATED, so the configuration is baked into the
-- stored tsvector: it cannot be changed with a query-level cast. Dropping and
-- re-adding the column rewrites the table and reindexes it. On a seeded demo
-- database that is instant; on a large table it takes an ACCESS EXCLUSIVE
-- lock, which is why this is a deliberate migration rather than an edit to
-- 00003 (which existing databases have already run).
ALTER TABLE products DROP COLUMN IF EXISTS search_vector;
ALTER TABLE products ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('indonesian', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('indonesian', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('indonesian', coalesce(slug, '')), 'C')
) STORED;
CREATE INDEX IF NOT EXISTS idx_products_search ON products USING GIN (search_vector);

-- Help articles use an expression index rather than a stored column, so it is
-- enough to replace the index; the queries also change their config.
DROP INDEX IF EXISTS idx_help_articles_search;
CREATE INDEX idx_help_articles_search ON help_articles
    USING GIN (to_tsvector('indonesian', title || ' ' || excerpt || ' ' || content));

-- +goose Down
DROP INDEX IF EXISTS idx_help_articles_search;
CREATE INDEX idx_help_articles_search ON help_articles
    USING GIN (to_tsvector('english', title || ' ' || excerpt || ' ' || content));

ALTER TABLE products DROP COLUMN IF EXISTS search_vector;
ALTER TABLE products ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(slug, '')), 'C')
) STORED;
CREATE INDEX IF NOT EXISTS idx_products_search ON products USING GIN (search_vector);
