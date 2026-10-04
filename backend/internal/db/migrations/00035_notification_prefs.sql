-- +goose Up
CREATE TABLE IF NOT EXISTS notification_prefs (
    user_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category VARCHAR(24) NOT NULL CHECK (category IN ('order', 'payment', 'price_alert', 'back_in_stock', 'store_new_product', 'low_stock', 'marketing')),
    in_app   BOOLEAN NOT NULL DEFAULT TRUE,
    email    BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (user_id, category)
);

-- +goose Down
DROP TABLE IF EXISTS notification_prefs;
