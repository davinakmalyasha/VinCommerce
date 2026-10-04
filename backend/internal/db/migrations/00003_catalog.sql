-- +goose Up
CREATE TABLE IF NOT EXISTS categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id  UUID REFERENCES categories (id) ON DELETE SET NULL,
    name       VARCHAR(120) NOT NULL,
    slug       VARCHAR(140) NOT NULL UNIQUE,
    path       TEXT NOT NULL DEFAULT '',
    depth      INT NOT NULL DEFAULT 0,
    position   INT NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_categories_parent ON categories (parent_id);
CREATE INDEX IF NOT EXISTS idx_categories_path ON categories (path);

CREATE TABLE IF NOT EXISTS brands (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(120) NOT NULL UNIQUE,
    slug       VARCHAR(140) NOT NULL UNIQUE,
    logo_url   TEXT,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS attributes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(80) NOT NULL UNIQUE,
    slug          VARCHAR(90) NOT NULL UNIQUE,
    filterable    BOOLEAN NOT NULL DEFAULT TRUE,
    searchable    BOOLEAN NOT NULL DEFAULT FALSE,
    position      INT NOT NULL DEFAULT 0,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS attribute_values (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_id UUID NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    value        VARCHAR(120) NOT NULL,
    slug         VARCHAR(140) NOT NULL,
    position     INT NOT NULL DEFAULT 0,
    UNIQUE (attribute_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_attr_values_attr ON attribute_values (attribute_id);

CREATE TABLE IF NOT EXISTS products (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id        UUID NOT NULL REFERENCES users (id),
    category_id      UUID REFERENCES categories (id),
    brand_id         UUID REFERENCES brands (id),
    name             VARCHAR(200) NOT NULL,
    slug             VARCHAR(220) NOT NULL UNIQUE,
    description      TEXT NOT NULL DEFAULT '',
    status           VARCHAR(16) NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'active', 'inactive', 'rejected')),
    attributes       JSONB NOT NULL DEFAULT '{}',
    seo_title        VARCHAR(160),
    seo_description  VARCHAR(320),
    avg_rating       NUMERIC(3,2) NOT NULL DEFAULT 0,
    rating_count     INT NOT NULL DEFAULT 0,
    sold_count       INT NOT NULL DEFAULT 0,
    search_vector    tsvector GENERATED ALWAYS AS (
                       setweight(to_tsvector('english', coalesce(name,'')), 'A') ||
                       setweight(to_tsvector('english', coalesce(description,'')), 'B') ||
                       setweight(to_tsvector('english', coalesce(slug,'')), 'C')
                     ) STORED,
    published_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_products_seller ON products (seller_id);
CREATE INDEX IF NOT EXISTS idx_products_category ON products (category_id);
CREATE INDEX IF NOT EXISTS idx_products_status ON products (status) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_search ON products USING GIN (search_vector);
CREATE INDEX IF NOT EXISTS idx_products_name_trgm ON products USING GIN (name gin_trgm_ops);

CREATE TABLE IF NOT EXISTS product_variants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    sku             VARCHAR(80) NOT NULL UNIQUE,
    name            VARCHAR(160) NOT NULL DEFAULT '',
    price           NUMERIC(14,2) NOT NULL CHECK (price >= 0),
    compare_at_price NUMERIC(14,2) CHECK (compare_at_price >= 0),
    stock           INT NOT NULL DEFAULT 0 CHECK (stock >= 0),
    weight_grams    INT NOT NULL DEFAULT 0,
    image_url       TEXT,
    attributes      JSONB NOT NULL DEFAULT '{}',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_variants_product ON product_variants (product_id);

CREATE TABLE IF NOT EXISTS product_images (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    url        TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (product_id, position)
);

CREATE TABLE IF NOT EXISTS product_reviews (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users (id),
    order_item_id UUID, -- FK to orders added once the orders table exists (migration 00004)
    rating       INT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    title        VARCHAR(160),
    content      TEXT NOT NULL DEFAULT '',
    images       JSONB NOT NULL DEFAULT '[]',
    status       VARCHAR(16) NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'approved', 'rejected')),
    admin_note   TEXT,
    helpful_count INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, order_item_id)
);

CREATE INDEX IF NOT EXISTS idx_reviews_product ON product_reviews (product_id, status);
CREATE INDEX IF NOT EXISTS idx_reviews_user ON product_reviews (user_id);

CREATE TABLE IF NOT EXISTS review_helpful (
    review_id UUID NOT NULL REFERENCES product_reviews (id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    helpful   BOOLEAN NOT NULL,
    PRIMARY KEY (review_id, user_id)
);

-- Keep product rating aggregates consistent via trigger.
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
    RETURN COALESCE(NEW, OLD);
END;
$func$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_recalc_rating
AFTER INSERT OR UPDATE OF status, rating OR DELETE ON product_reviews
FOR EACH ROW EXECUTE FUNCTION recalc_product_rating();

-- +goose Down
DROP TRIGGER IF EXISTS trg_recalc_rating ON product_reviews;
DROP FUNCTION IF EXISTS recalc_product_rating();
DROP TABLE IF EXISTS review_helpful;
DROP TABLE IF EXISTS product_reviews;
DROP TABLE IF EXISTS product_images;
DROP TABLE IF EXISTS product_variants;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS attribute_values;
DROP TABLE IF EXISTS attributes;
DROP TABLE IF EXISTS brands;
DROP TABLE IF EXISTS categories;
