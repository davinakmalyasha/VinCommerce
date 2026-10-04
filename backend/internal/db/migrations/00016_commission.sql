-- +goose Up
CREATE TABLE IF NOT EXISTS platform_fees (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pct        NUMERIC(5,2) NOT NULL DEFAULT 2.00,
    fixed      NUMERIC(14,2) NOT NULL DEFAULT 0,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO platform_fees (id, pct, fixed, is_active) VALUES (gen_random_uuid(), 2.00, 0, TRUE);

ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS fee_amount   NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS seller_amount NUMERIC(14,2) NOT NULL DEFAULT 0;

-- internal platform account that accumulates commission income
INSERT INTO users (id, email, phone, full_name, password_hash, roles, status, email_verified_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'platform@vincommerce.local', NULL, 'VinCommerce Platform',
        '$argon2id$v=19$m=65536,t=3,p=2$c2VlZGluZy1vbmx5LWFjY291bnQ$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', '{admin}', 'active', now())
ON CONFLICT (id) DO NOTHING;

INSERT INTO wallets (user_id, balance, held_balance)
VALUES ('00000000-0000-0000-0000-000000000001', 0, 0)
ON CONFLICT (user_id) DO NOTHING;

-- +goose Down
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS fee_amount,
    DROP COLUMN IF EXISTS seller_amount;
DROP TABLE IF EXISTS platform_fees;
