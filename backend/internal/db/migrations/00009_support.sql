-- +goose Up
CREATE TABLE IF NOT EXISTS help_categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(80) NOT NULL,
    slug       VARCHAR(100) NOT NULL UNIQUE,
    position   INT NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS help_articles (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id  UUID REFERENCES help_categories (id) ON DELETE SET NULL,
    title        VARCHAR(200) NOT NULL,
    slug         VARCHAR(220) NOT NULL UNIQUE,
    excerpt      VARCHAR(400) NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    section      VARCHAR(16) NOT NULL DEFAULT 'help'
                 CHECK (section IN ('help', 'docs', 'legal')),
    is_published BOOLEAN NOT NULL DEFAULT FALSE,
    view_count   INT NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_help_articles_section ON help_articles (section, is_published);
CREATE INDEX IF NOT EXISTS idx_help_articles_search ON help_articles USING GIN (to_tsvector('english', title || ' ' || excerpt || ' ' || content));

CREATE SEQUENCE ticket_number_seq START 1000;

CREATE TABLE IF NOT EXISTS support_tickets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_number VARCHAR(24) NOT NULL UNIQUE,
    user_id       UUID NOT NULL REFERENCES users (id),
    order_id      UUID REFERENCES orders (id) ON DELETE SET NULL,
    subject       VARCHAR(200) NOT NULL,
    category      VARCHAR(40) NOT NULL DEFAULT 'general',
    priority      VARCHAR(10) NOT NULL DEFAULT 'normal'
                  CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    status        VARCHAR(16) NOT NULL DEFAULT 'open'
                  CHECK (status IN ('open', 'in_progress', 'resolved', 'closed')),
    assigned_to   UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_tickets_user ON support_tickets (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON support_tickets (status);
CREATE INDEX IF NOT EXISTS idx_tickets_assignee ON support_tickets (assigned_to) WHERE assigned_to IS NOT NULL;

CREATE TABLE IF NOT EXISTS ticket_messages (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   UUID NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    author_id   UUID NOT NULL REFERENCES users (id),
    author_role VARCHAR(16) NOT NULL,
    body        TEXT NOT NULL,
    is_internal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON ticket_messages (ticket_id, created_at);

CREATE TABLE IF NOT EXISTS notifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       VARCHAR(32) NOT NULL,
    title      VARCHAR(200) NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    data       JSONB NOT NULL DEFAULT '{}',
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS ticket_messages;
DROP TABLE IF EXISTS support_tickets;
DROP SEQUENCE IF EXISTS ticket_number_seq;
DROP TABLE IF EXISTS help_articles;
DROP TABLE IF EXISTS help_categories;
