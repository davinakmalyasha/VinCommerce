-- +goose Up
CREATE TABLE IF NOT EXISTS payment_intents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         UUID NOT NULL REFERENCES orders (id),
    buyer_id         UUID NOT NULL REFERENCES users (id),
    amount           NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    currency         VARCHAR(3) NOT NULL DEFAULT 'IDR',
    status           VARCHAR(16) NOT NULL DEFAULT 'initiated'
                     CHECK (status IN ('initiated', 'authorized', 'captured', 'released',
                                       'refunded', 'partially_refunded', 'failed', 'expired')),
    gateway          VARCHAR(24) NOT NULL,
    gateway_ref      VARCHAR(64),
    method           VARCHAR(32),
    idempotency_key  VARCHAR(64) NOT NULL UNIQUE,
    escrow_released_at TIMESTAMPTZ,
    captured_at      TIMESTAMPTZ,
    refunded_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_intents_order ON payment_intents (order_id);
CREATE INDEX IF NOT EXISTS idx_payment_intents_buyer ON payment_intents (buyer_id);

CREATE TABLE IF NOT EXISTS wallets (
    user_id       UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    balance       NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    held_balance  NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (held_balance >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
    id            BIGSERIAL PRIMARY KEY,
    wallet_id     UUID NOT NULL REFERENCES wallets (user_id) ON DELETE CASCADE,
    kind          VARCHAR(10) NOT NULL CHECK (kind IN ('credit', 'debit')),
    reason        VARCHAR(32) NOT NULL,
    amount        NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    balance_after NUMERIC(14,2) NOT NULL,
    ref_id        VARCHAR(64),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wallet_tx_wallet ON wallet_transactions (wallet_id, created_at DESC);

CREATE TABLE IF NOT EXISTS payouts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id     UUID NOT NULL REFERENCES wallets (user_id) ON DELETE CASCADE,
    amount        NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    status        VARCHAR(16) NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'processing', 'sent', 'failed', 'cancelled')),
    gateway_ref   VARCHAR(64),
    bank_name     VARCHAR(80),
    bank_account  VARCHAR(40),
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_payouts_wallet ON payouts (wallet_id, requested_at DESC);

-- +goose Down
DROP TABLE IF EXISTS payouts;
DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS payment_intents;
