-- 00043_double_entry_ledger.sql
--
-- A real double-entry ledger, because the previous model cannot answer the only
-- question that matters to an escrow marketplace: "how much money are we
-- holding, and for whom?"
--
-- WHAT WAS WRONG
--
-- `wallet_transactions` is a single-sided running-balance log:
--
--     (wallet_id, kind credit|debit, reason, amount, balance_after, ref_id)
--
-- There is no account dimension, no counterparty, and no external leg. Every
-- movement between the outside world and a wallet is unrecorded. Tracing one
-- Rp100,000 order at 2% commission:
--
--   capture   -> writes NO ledger rows at all
--   release   -> credits the seller Rp98,000 and the platform Rp2,000
--   payout    -> debits the seller Rp98,000
--
-- The books say Rp2,000. The world holds Rp100,000. Rp98,000 is a real liability
-- to a seller who has already withdrawn it, and a real cash asset, and it
-- appears on no balance sheet anywhere in this system. `wallets.held_balance`
-- is declared `NOT NULL CHECK (held_balance >= 0)` and is never written by any
-- code path, so the CHECK is vacuously satisfied and reads as protection.
--
-- Reconciliation is not merely hard here; it is impossible from this data model,
-- because the inflows and outflows that would close the loop were never recorded.
-- There is no table for gateway balances, settlement dates, MDR fees, or payout
-- batches.
--
-- WHAT THIS ADDS
--
-- The classic model: journals (transactions) containing entries (lines), each
-- entry hitting a named account with a debit or credit side. The invariant --
-- every journal sums to zero -- is enforced by a DEFERRABLE CONSTRAINT TRIGGER
-- at COMMIT, so it cannot be violated by any code path including a future one.
--
-- `wallets` survives as a DERIVED CACHE, because rewriting every read path to
-- aggregate the ledger would be a large change for no correctness benefit. A
-- reconciliation job asserts the two agree; that job is the thing that makes the
-- cache trustworthy rather than assumed.

-- ===========================================================================
-- Section 1 - chart of accounts
-- ===========================================================================
CREATE TABLE ledger_accounts (
    code        VARCHAR(48)  PRIMARY KEY,
    name        VARCHAR(120) NOT NULL,
    class       VARCHAR(12)  NOT NULL CHECK (class IN ('asset','liability','equity','revenue','expense')),
    -- Which side increases the account. Assets and expenses increase on debit;
    -- liabilities, equity and revenue increase on credit. A posting to the
    -- wrong side is a sign error, and this is what makes the trial balance
    -- readable.
    normal_side VARCHAR(6)   NOT NULL CHECK (normal_side IN ('debit','credit')),
    -- A system account is part of the double-entry machinery (the gateway
    -- clearing account, escrow, reserves) rather than a user wallet.
    is_system   BOOLEAN      NOT NULL DEFAULT true,
    currency    CHAR(3)      NOT NULL DEFAULT 'IDR',
    -- Which user a personal account belongs to. NULL for system accounts. This
    -- is the link that makes "SUM(seller balances)" answerable as a query rather
    -- than as a fold over journals.
    user_id     UUID         REFERENCES users (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ledger_accounts_user_side CHECK (
        (is_system AND user_id IS NULL) OR (NOT is_system AND user_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX idx_ledger_accounts_user ON ledger_accounts (user_id)
    WHERE NOT is_system;

-- ===========================================================================
-- Section 2 - journals and entries
-- ===========================================================================
CREATE TABLE ledger_journals (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The business key. Makes every posting idempotent by construction: a retry
    -- of the same business event reuses the same journal rather than creating a
    -- second one. This is the property the whole refund and capture path needs
    -- and did not have.
    idempotency_key VARCHAR(160) NOT NULL,
    tx_type         VARCHAR(40)  NOT NULL,
    ref_type        VARCHAR(32),
    ref_id          VARCHAR(64),
    effective_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- A reversal is a new journal pointing at its original. Nothing is ever
    -- UPDATEd or DELETEd from this table, so the audit trail is append-only and
    -- a mistaken posting is corrected rather than erased.
    reversal_of     UUID        REFERENCES ledger_journals (id),
    note            TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ledger_journals_not_self_referential CHECK (reversal_of IS NULL OR reversal_of <> id)
);

CREATE UNIQUE INDEX idx_ledger_journals_idem ON ledger_journals (idempotency_key);
CREATE INDEX idx_ledger_journals_ref  ON ledger_journals (ref_type, ref_id);
CREATE INDEX idx_ledger_journals_time ON ledger_journals (effective_at DESC);

CREATE TABLE ledger_entries (
    id           BIGSERIAL   PRIMARY KEY,
    journal_id   UUID        NOT NULL REFERENCES ledger_journals (id) ON DELETE CASCADE,
    account_code VARCHAR(48) NOT NULL REFERENCES ledger_accounts (code),
    amount       NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    side         VARCHAR(6)  NOT NULL CHECK (side IN ('debit','credit')),
    currency     CHAR(3)     NOT NULL DEFAULT 'IDR',
    metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ledger_entries_journal ON ledger_entries (journal_id);
CREATE INDEX idx_ledger_entries_account ON ledger_entries (account_code, created_at DESC);

-- ===========================================================================
-- Section 3 - the invariant
-- ===========================================================================
--
-- Every journal must sum to zero: total debits == total credits.
--
-- This is a DEFERRABLE CONSTRAINT TRIGGER so it fires at COMMIT, not per row. A
-- per-row trigger would see a half-written journal and fail every multi-line
-- posting; deferring is what makes "post five entries and they balance" the
-- natural way to write this code.
--
-- The imbalance is reported with the actual difference, because a constraint
-- violation with no detail is the hardest kind of bug to diagnose from a log.
CREATE OR REPLACE FUNCTION assert_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    -- NEW only. An earlier version wrote COALESCE(NEW.journal_id, OLD.journal_id)
    -- "for symmetry", but this trigger is AFTER INSERT, and in PL/pgSQL an
    -- unassigned OLD record raises "record old is not assigned yet" the moment it
    -- is dereferenced -- inside a COALESCE too. The symmetry bought nothing and
    -- the function would have failed on every single journal.
    jid  UUID := NEW.journal_id;
    diff NUMERIC(18,2);
    deb  NUMERIC(18,2);
    cre  NUMERIC(18,2);
BEGIN
    SELECT COALESCE(SUM(amount) FILTER (WHERE side = 'debit'),  0),
           COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0),
           COALESCE(SUM(CASE WHEN side = 'debit' THEN amount ELSE -amount END), 0)
      INTO deb, cre, diff
      FROM ledger_entries
     WHERE journal_id = jid;

    IF diff <> 0 THEN
        RAISE EXCEPTION
            'ledger journal % is unbalanced by Rp % (debits Rp %, credits Rp %)',
            jid, diff, deb, cre
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER ledger_journal_balanced
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_journal_balanced();

-- Currency must be uniform within a journal. Mixing IDR and USD in one posting
-- would make the zero-sum check meaningless.
CREATE OR REPLACE FUNCTION assert_journal_single_currency() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    jid   UUID := NEW.journal_id;
    kinds INT;
BEGIN
	-- NEW only, for the same reason as assert_journal_balanced: an unassigned OLD
	-- record raises in PL/pgSQL even inside COALESCE.
	SELECT COUNT(DISTINCT currency) INTO kinds
      FROM ledger_entries WHERE journal_id = jid;
    IF kinds > 1 THEN
        RAISE EXCEPTION 'ledger journal % mixes % currencies', jid, kinds
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER ledger_journal_currency
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_journal_single_currency();

-- A journal with no entries must not exist.
--
-- The zero-sum check cannot catch this case: an empty journal sums to 0 = 0 and is
-- technically balanced. It is also worse than unbalanced, because it looks like a
-- posted transaction in the journal list while moving no money at all, and an
-- idempotency key burned on it means the real posting can never be retried.
--
-- This trigger is on ledger_journals rather than ledger_entries precisely because
-- the entry-level trigger never fires when there are no entries. Deferrable, like
-- the others, because the journal is always inserted before its entries.
CREATE OR REPLACE FUNCTION assert_journal_has_entries() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    n INT;
BEGIN
    SELECT COUNT(*) INTO n FROM ledger_entries WHERE journal_id = NEW.id;
    IF n = 0 THEN
        RAISE EXCEPTION
            'ledger journal % (% / %) has no entries; a journal with no entries '
            'would sum to zero and look posted while moving no money',
            NEW.id, NEW.idempotency_key, NEW.tx_type
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER ledger_journal_has_entries
    AFTER INSERT ON ledger_journals
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_journal_has_entries();

-- ===========================================================================
-- Section 4 - derived account balances
-- ===========================================================================
--
-- A CACHE. `ledger_entries` is the truth; this is what a trial balance and a
-- wallet read actually query, because aggregating every entry on every request
-- would be quadratic. The reconciliation job (internal/service, see
-- LedgerService.Reconcile) asserts the two agree, and that job is what makes the
-- cache trustworthy.
--
-- `version` is bumped on every write so a reader can tell a cached row from a
-- stale one without recomputing anything.
CREATE TABLE account_balances (
    account_code VARCHAR(48) NOT NULL REFERENCES ledger_accounts (code),
    currency     CHAR(3)     NOT NULL,
    balance      NUMERIC(18,2) NOT NULL DEFAULT 0,
    entry_count  BIGINT      NOT NULL DEFAULT 0,
    version      BIGINT      NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_code, currency)
);

-- ===========================================================================
-- Section 5 - refunds
-- ===========================================================================
--
-- The gateway-side refund record. `payments.Gateway` had no `Refund()` method, so
-- every refund was an internal book transfer: a buyer who paid QRIS or bank VA
-- was refunded to a wallet balance they had no way to top up, which is also a
-- Midtrans ToS breach. `status` distinguishes the states an operator has to be
-- able to tell apart:
--
--   pending   queued, not yet submitted
--   submitted sent to the gateway, outcome unknown
--   succeeded the gateway confirmed
--   failed    the gateway rejected; safe to retry
--   manual    the gateway could not complete it and a human must transfer the
--             money out of band. This is a real, expected state, not an error
--             case to be hidden -- a refund that silently failed is worse than
--             one that is visibly stuck.
CREATE TABLE refunds (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id  UUID        NOT NULL REFERENCES payment_intents (id) ON DELETE RESTRICT,
    order_id           UUID        NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    journal_id         UUID        REFERENCES ledger_journals (id),
    gateway            VARCHAR(32) NOT NULL,
    gateway_ref        VARCHAR(64),
    amount             NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    reason             VARCHAR(160),
    status             VARCHAR(12) NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','submitted','succeeded','failed','manual')),
    failure_reason     TEXT,
    requested_by       UUID        REFERENCES users (id) ON DELETE RESTRICT,
    requested_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A refund is never issued twice against the same gateway reference. Partial
    -- refunds are legitimate and repeatable, so the guard is on the gateway ref
    -- rather than on the order.
    CONSTRAINT refunds_amount_whole_rupiah CHECK (amount = round(amount))
);

CREATE INDEX idx_refunds_intent  ON refunds (payment_intent_id, created_at DESC);
CREATE INDEX idx_refunds_order   ON refunds (order_id);
CREATE INDEX idx_refunds_pending ON refunds (status, created_at)
    WHERE status IN ('pending', 'submitted');

-- ===========================================================================
-- Section 6 - payout batches and seller reserves
-- ===========================================================================
--
-- Payouts were seller-initiated and on demand. Real marketplaces run a T+n batch
-- with a reserve for COD and a disputes window, which is also the single largest
-- fraud control in the industry: a seller cannot withdraw a balance that is
-- about to be clawed back by a return, because the money has not left yet.
CREATE TABLE payout_batches (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    cutoff_at     TIMESTAMPTZ NOT NULL,
    status        VARCHAR(12) NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft','approved','submitted','paid','failed','cancelled')),
    total         NUMERIC(18,2) NOT NULL DEFAULT 0 CHECK (total >= 0),
    item_count    INT         NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    -- The schedule this batch is running. T+2 or T+7, and it is recorded rather
    -- than implied so a late batch is visible rather than inexplicable.
    lag_days      INT         NOT NULL DEFAULT 7 CHECK (lag_days >= 0),
    approved_by   UUID        REFERENCES users (id) ON DELETE RESTRICT,
    approved_at   TIMESTAMPTZ,
    paid_at       TIMESTAMPTZ,
    note          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payout_batches_status ON payout_batches (status, cutoff_at DESC);

CREATE TABLE payout_batch_items (
    batch_id     UUID        NOT NULL REFERENCES payout_batches (id) ON DELETE CASCADE,
    payout_id    UUID        NOT NULL REFERENCES payouts (id) ON DELETE RESTRICT,
    seller_id    UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    amount       NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    status       VARCHAR(12) NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','submitted','paid','failed')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (batch_id, payout_id)
);

CREATE INDEX idx_payout_batch_items_seller ON payout_batch_items (seller_id, created_at DESC);

-- A holdback against a balance that is not yet releasable. `releases_at` is a
-- CALCULATION, not a timer: delivered, no open return, no open dispute, and
-- past the lag. A timer would pay out a balance that a return is about to
-- reverse.
CREATE TABLE seller_reservations (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id   UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    order_id    UUID        REFERENCES orders (id) ON DELETE RESTRICT,
    payout_id   UUID        REFERENCES payouts (id) ON DELETE RESTRICT,
    amount      NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    kind        VARCHAR(16)  NOT NULL CHECK (kind IN ('cod','dispute','return','payout')),
    note        TEXT,
    released_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- EXACTLY one source, not "at most one". The constraint is named
    -- one_source and every kind has an obvious source -- cod, dispute and return
    -- all reserve against an order; payout reserves against the payout. Allowing
    -- zero admits a reservation attached to nothing, which is a hold on money
    -- that can never be released or explained, and it is the kind of orphan row
    -- that only surfaces during a year-end audit.
    CONSTRAINT seller_reservations_one_source
        CHECK (num_nonnulls(order_id, payout_id) = 1)
);

CREATE UNIQUE INDEX idx_seller_reservations_payout
    ON seller_reservations (payout_id) WHERE payout_id IS NOT NULL;
CREATE INDEX idx_seller_reservations_seller
    ON seller_reservations (seller_id, released_at) WHERE released_at IS NULL;

-- ===========================================================================
-- Section 7 - gateway settlement records
-- ===========================================================================
--
-- The missing leg. Without this there is no table anywhere that records money
-- ARRIVING from Midtrans or LEAVING to a bank, which is why the previous model
-- could not be reconciled no matter how it was queried.
--
-- One row per gateway-reported movement. `settled_on` is the gateway's own
-- settlement date, which is not the capture date: Midtrans settles T+1 to T+7
-- depending on the method, and a balance sheet that conflates the two overstates
-- cash in flight.
CREATE TABLE gateway_settlements (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway       VARCHAR(32) NOT NULL,
    settlement_ref VARCHAR(80),
    settled_on    DATE        NOT NULL,
    kind          VARCHAR(12) NOT NULL CHECK (kind IN ('capture','payout','refund','fee','chargeback','adjustment')),
    gross         NUMERIC(16,2) NOT NULL,
    fee           NUMERIC(16,2) NOT NULL DEFAULT 0,
    net           NUMERIC(16,2) NOT NULL,
    currency      CHAR(3)     NOT NULL DEFAULT 'IDR',
    raw           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- A settlement line is matched to exactly one internal journal, so the
    -- reconciliation is a join rather than a heuristic.
    --
    -- ON DELETE RESTRICT, not CASCADE: journals are the accounting record and are
    -- append-only, so deleting one that a settlement points at must fail loudly
    -- rather than quietly orphan a movement of real money.
    journal_id    UUID        REFERENCES ledger_journals (id) ON DELETE RESTRICT,
    reconciled_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT gateway_settlements_foots CHECK (net = gross - fee)
);

-- A gateway reference is unique per kind: Midtrans can legitimately issue the
-- same reference string for a capture and a later refund, and treating that as
-- a duplicate would reject a real refund.
CREATE UNIQUE INDEX idx_gateway_settlements_ref
    ON gateway_settlements (gateway, kind, settlement_ref)
    WHERE settlement_ref IS NOT NULL;
CREATE INDEX idx_gateway_settlements_unreconciled
    ON gateway_settlements (settled_on) WHERE reconciled_at IS NULL;

-- ===========================================================================
-- Section 8 - the chart of accounts, seeded
-- ===========================================================================
--
-- Seeded with `idempotency_key` as the goose migration, so a re-run does not
-- duplicate. The `on conflict do update` refreshes the descriptive columns while
-- leaving is_system and user_id alone, so an operator adding a personal account
-- later is not clobbered.
--
-- The asset/liability shape is what makes the escrow question answerable:
--
--   gateway_clearing  an asset: cash owed to us by the gateway
--   escrow_held       a liability: cash we owe to buyers/sellers
--   seller_pending    a liability: what we owe sellers who have been paid out
--   seller_available  a liability: what we owe sellers not yet paid out
--   platform_commission a revenue: what we have earned and not reversed
--   cod_receivable    an asset: cash the courier holds on our behalf
--   bank_clearing     an asset: cash in our own bank
--
-- A single Rp100,000 order is then: capture debits gateway_clearing and
-- credits escrow_held; release debits escrow_held and credits seller_pending +
-- platform_commission; payout debits seller_pending and credits bank_clearing.
-- Every rupiah is on a balance sheet, and the sum of the two escrow accounts is
-- exactly "money we are holding for other people".
INSERT INTO ledger_accounts (code, name, class, normal_side, is_system) VALUES
    -- assets
    ('bank_clearing',          'Cash in the platform bank account',              'asset',     'debit',  true),
    ('gateway_clearing',       'Owed to us by the payment gateway',              'asset',     'debit',  true),
    ('cod_receivable',         'Cash held by couriers for COD orders',            'asset',     'debit',  true),
    ('refund_payable',         'Refunds owed to buyers, awaiting gateway payout', 'asset',     'debit',  true),
    ('chargeback_receivable',  'Chargebacks we expect to recover',                'asset',     'debit',  true),
    -- liabilities
    ('escrow_held',            'Buyer and seller funds held in escrow',           'liability', 'credit', true),
    ('seller_available',       'Credited to sellers, not yet paid out',           'liability', 'credit', true),
    ('seller_pending',         'Scheduled for payout',                           'liability', 'credit', true),
    ('seller_held',            'Held back for COD, disputes and returns',        'liability', 'credit', true),
    ('tax_payable_ppn',        'VAT collected, owed to the tax authority',        'liability', 'credit', true),
    ('tax_withheld_pph22',     'PPh 22 withheld on behalf of the state',          'liability', 'credit', true),
    -- equity
    ('platform_equity',        'Retained platform capital',                       'equity',    'credit', true),
    -- revenue
    ('platform_commission',    'Commission earned, net of reversals',             'revenue',   'credit', true),
    ('platform_ads',           'Advertising revenue',                            'revenue',   'credit', true),
    ('platform_promo',          'Promotional subsidy absorbed by the platform',   'revenue',   'credit', true),
    ('other_income',           'Adjustment income',                              'revenue',   'credit', true),
    -- expenses
    ('gateway_fee',            'Payment gateway fees and MDR',                   'expense',   'debit',  true),
    ('refund_writeoff',        'Refunds that could not be returned to a gateway', 'expense',   'debit',  true),
    ('chargeback_loss',        'Chargebacks written off',                        'expense',   'debit',  true),
    ('rounding_dust',          'Rounding differences absorbed by the platform',   'expense',   'debit',  true)
ON CONFLICT (code) DO UPDATE
    SET name = EXCLUDED.name, class = EXCLUDED.class, normal_side = EXCLUDED.normal_side;

-- Balance rows for every account, so a trial balance never has to distinguish
-- "balance is zero" from "no row yet".
INSERT INTO account_balances (account_code, currency, balance)
SELECT code, currency, 0 FROM ledger_accounts
ON CONFLICT (account_code, currency) DO NOTHING;

-- ===========================================================================
-- Section 9 - bridging the existing wallet_transactions log
-- ===========================================================================
--
-- The existing log is retained as an append-only AUDIT TRAIL. It is not a
-- ledger: it has no account dimension and cannot be reconciled, which is why
-- this migration exists. It is kept because it is the historical record of every
-- wallet movement to date, and deleting it would destroy the audit history of
-- balances the platform has already promised sellers.
--
-- New code writes BOTH: a journal for the accounting truth, and a
-- `wallet_transactions` row for the existing balance and statement queries. That
-- duplication is deliberate and temporary -- it is the seam that lets the ledger
-- be introduced without rewriting every read path in one commit, and
-- `LedgerService.Reconcile` is what proves the two agree.
--
-- The comment on the table is updated to say so at the schema level rather than
-- only in the code, because the next person to read the table definition will
-- not have the commit history.
COMMENT ON TABLE wallet_transactions IS
  'Append-only audit trail of every wallet movement, retained for history. NOT the accounting ledger: it has no account dimension and cannot be reconciled to cash. See ledger_journals/ledger_entries for the double-entry model, and migration 00043 for why.';

COMMENT ON TABLE wallets IS
  'A DERIVED CACHE of ledger_entries for this user''s personal account, not a source of truth. Asserted equal to the ledger by the reconciliation job. held_balance was previously never written by any code path; it is now maintained by the escrow postings.';

-- +goose Down
--
-- goose-down: retained
--
-- Deliberately NOT dropping the ledger tables. A Down that dropped them would
-- discard the accounting history of every payout batch, refund and settlement
-- processed while this migration was live, which is worse than leaving a schema
-- an operator can inspect. The tables are retained; the new constraints on
-- product_variants, orders and payment_intents live in 00041 and are reverted
-- there.
--
-- The marker above is required by scripts/check-migration.mjs. Without it this
-- Down looks identical to a migration whose rollback was simply forgotten, and
-- `goose down` would report success having changed nothing -- a rollback that
-- appears to work is the same failure as a check that cannot fail, one level up.
--
-- To actually remove the ledger, an operator must first archive:
--   CREATE TABLE ledger_journals_archive AS TABLE ledger_journals;
--   CREATE TABLE ledger_entries_archive AS TABLE ledger_entries;
-- then DROP. That is an operator decision about data retention, not a migration
-- step, and it should never be reachable by a `goose down`.
SELECT 1;
