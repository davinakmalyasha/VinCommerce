-- 00046: make the ledger able to hold a seller's HELD balance
--
-- Two defects, both of which have silently disabled double-entry bookkeeping for
-- every money movement that touches a seller. Neither has ever fired, because no
-- database has ever run these migrations -- which is the point: they are exactly
-- the class of thing an unexecuted migration hides.
--
-- 1. THE ACCOUNT CODE COLUMN IS TOO NARROW.
--
--    ledger_accounts.code              VARCHAR(48)
--    ledger_entries.account_code       VARCHAR(48)
--    account_balances.account_code     VARCHAR(48)
--
--    and a personal account code is the namespace plus a uuid:
--
--    "seller_available:" + "3f2504e0-4f89-41d3-9a0c-0305e82c3301"  =  53
--
--    Every posting to a personal account therefore failed on width. And
--    `postLedger` logs a failed journal and returns nothing, so the wallet
--    movement committed with no journal behind it: escrow release credited the
--    seller, the commission was booked, and the ledger recorded neither. The
--    reconciliation job was reading `wallets.balance` against an account that
--    could never exist, so it would have reported drift on every seller from the
--    first payout onwards -- which, in a system nobody had run yet, was nobody's
--    problem to notice.
--
--    64 is the smallest width that fits the longest name we compose
--    ("seller_available:" + uuid = 53) with room to spare, and it keeps the
--    namespace convention readable in a psql session.
--
-- 2. A SELLER COULD NOT HAVE BOTH ACCOUNTS.
--
--    CREATE UNIQUE INDEX idx_ledger_accounts_user ON ledger_accounts (user_id)
--        WHERE NOT is_system;
--
--    At most ONE non-system account per user. `seller_available:<id>` already
--    claims that slot, so `seller_held:<id>` could not be created -- even though
--    `AccSellerHeld` was declared for exactly that purpose and
--    `reconcileWallets` already LEFT JOINs `account_balances` on the code it
--    composes. The design was drawn and three of its four moving parts were never
--    built.
--
--    `purpose` distinguishes the two claims on the same money: AVAILABLE is money
--    the seller may withdraw, HELD is money a return, dispute or uncollected COD
--    order could still reverse. A state column on one account was the alternative
--    and is worse: "what can this seller withdraw" would become a question about a
--    row rather than a balance, and the balance is what the payout path reads.

-- +goose Up

-- Widen. Widening a VARCHAR is non-destructive and needs no table rewrite of the
-- data; the existing codes are all far shorter than 64.
ALTER TABLE ledger_accounts      ALTER COLUMN code              TYPE VARCHAR(64);
ALTER TABLE ledger_entries       ALTER COLUMN account_code       TYPE VARCHAR(64);
ALTER TABLE account_balances     ALTER COLUMN account_code       TYPE VARCHAR(64);

-- The held account needs a row, so the "one personal account" rule has to become
-- "one personal account per purpose".
ALTER TABLE ledger_accounts
    ADD COLUMN purpose VARCHAR(16),
    ADD CONSTRAINT ledger_accounts_purpose
        CHECK (purpose IS NULL OR purpose IN ('available', 'held')),
    -- Replaces the intent of `ledger_accounts_user_side` from 00043, which said a
    -- system account has no user and a personal one must. Kept as a separate
    -- constraint rather than folded in so 00043's is still the one that reads
    -- "is this account a user's" and this one reads "and what kind".
    ADD CONSTRAINT ledger_accounts_purpose_side CHECK (
        (is_system     AND user_id IS NULL     AND purpose IS NULL) OR
        (NOT is_system AND user_id IS NOT NULL AND purpose IS NOT NULL)
    );

-- Backfill before the new unique index, which requires purpose to be populated.
-- Every personal account that exists today is a `seller_available:` one -- that is
-- the only namespace the service has ever composed -- so the split is not a
-- judgement call. The second statement is a guard rather than a routine update:
-- it names anything that is neither, so a new namespace cannot be introduced and
-- silently classified as spendable.
UPDATE ledger_accounts SET purpose = 'available'
 WHERE NOT is_system AND purpose IS NULL AND code LIKE 'seller_available:%';

DO $$
DECLARE
    unclassified TEXT;
BEGIN
    SELECT string_agg(code, ', ' ORDER BY code) INTO unclassified
      FROM ledger_accounts
     WHERE NOT is_system AND purpose IS NULL;

    IF unclassified IS NOT NULL THEN
        RAISE EXCEPTION
            'personal ledger account(s) % are neither seller_available nor seller_held, so this migration cannot tell which side of the seller''s balance they belong on. Classify them by hand and re-run.',
            unclassified;
    END IF;
END $$;

DROP INDEX idx_ledger_accounts_user;
CREATE UNIQUE INDEX idx_ledger_accounts_user
    ON ledger_accounts (user_id, purpose)
    WHERE NOT is_system;

-- The lookup the trial balance and the reconciliation do: a user's held balance,
-- which nothing could previously answer.
CREATE INDEX idx_ledger_accounts_purpose
    ON ledger_accounts (purpose)
    WHERE purpose IS NOT NULL;

-- +goose Down

-- Reverting this is best-effort and will FAIL once a seller holds two personal
-- accounts, because the old index permits one. That failure is the correct
-- outcome: a rollback that dropped the held account to satisfy a unique index
-- would delete the record of money the platform still owes, and the operator
-- would have to reconcile that by hand with no indication it had happened.
--
-- To actually revert, first move the held balances back to the spendable account
-- and delete those rows. That is an operator decision about money, not a
-- migration step.
DROP INDEX IF EXISTS idx_ledger_accounts_purpose;

ALTER TABLE ledger_accounts
    DROP CONSTRAINT IF EXISTS ledger_accounts_purpose_side,
    DROP CONSTRAINT IF EXISTS ledger_accounts_purpose,
    DROP COLUMN IF EXISTS purpose;

DROP INDEX IF EXISTS idx_ledger_accounts_user;
CREATE UNIQUE INDEX idx_ledger_accounts_user
    ON ledger_accounts (user_id)
    WHERE NOT is_system;
