-- +goose Up
-- Wallet top-up.
--
-- Until now a buyer wallet could only grow from a refund or a dispute split:
-- there was no way to put money in, which made "pay with wallet" unreachable
-- for a normal buyer and made a refunded balance impossible to top up again.
--
-- Two schema changes:
--   1. wallet_topups, keyed by the gateway reference our webhook receives.
--   2. a third personal ledger purpose, 'wallet', for the buyer's own money.
--      Buyer balance and seller earnings live in the same wallets row, but they
--      are different liabilities. Posting a top-up to seller_available would
--      make a buyer's deposit look like money owed to a seller.
ALTER TABLE ledger_accounts
    DROP CONSTRAINT IF EXISTS ledger_accounts_purpose;
ALTER TABLE ledger_accounts
    ADD CONSTRAINT ledger_accounts_purpose
        CHECK (purpose IS NULL OR purpose IN ('available', 'held', 'wallet'));

CREATE TABLE IF NOT EXISTS wallet_topups (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id),
    amount       NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    status       VARCHAR(16) NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'paid', 'failed', 'expired')),
    gateway      VARCHAR(24) NOT NULL,
    -- The provider's reference for the charge. UNIQUE is what makes a duplicate
    -- webhook a no-op instead of a second credit.
    gateway_ref  VARCHAR(64) NOT NULL UNIQUE,
    snap_token   TEXT,
    redirect_url TEXT,
    paid_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wallet_topups_user ON wallet_topups (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wallet_topups_status ON wallet_topups (status);

-- +goose Down
DROP TABLE IF EXISTS wallet_topups;
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS ledger_accounts_purpose;
ALTER TABLE ledger_accounts
    ADD CONSTRAINT ledger_accounts_purpose
        CHECK (purpose IS NULL OR purpose IN ('available', 'held'));
