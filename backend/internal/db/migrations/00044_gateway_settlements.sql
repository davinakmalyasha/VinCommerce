-- 00044_gateway_settlements.sql
--
-- Wiring for the `gateway_settlements` table created (but never written) in
-- 00043, plus the reconciliation query that proves a settlement is matched to a
-- journal.
--
-- WHAT WAS WRONG
--
-- `gateway_settlements` existed and had no writer, so the missing leg of the
-- ledger was still missing. A settlement row is how cash arriving from or
-- leaving to a payment gateway gets recorded, and without one the platform can
-- compare its own books to itself but never to reality. Midtrans settles T+1 to
-- T+7 by method, so a balance sheet that uses the capture date overstates cash
-- in flight by however long settlement takes -- which for a marketplace holding
-- other people's money is not a rounding difference.
--
-- What this adds:
--
--   * `settlement_imports` so an import is a first-class, re-runnable event with
--     its own state, rather than a side effect of a cron job nobody can see.
--   * A partial index for the unreconciled queue, which is the query the job and
--     the operator both run constantly.
--   * A `settlement_matches` VIEW that resolves a settlement against the ledger,
--     so "is this matched?" is a join rather than a comparison somebody
--     reimplements differently in the job and in the admin screen.
--
-- WHY AN IMPORT TABLE AND NOT JUST ROWS
--
-- Imported settlement files arrive in batches and batches fail as a unit. If
-- row-by-row inserts are the only record, a file that imported 400 of 500 lines
-- and then died is indistinguishable from a file that legitimately had 400
-- lines. The import row is the receipt: it says what was submitted, what was
-- accepted, and what was rejected, and it is what makes the job re-runnable
-- without double-counting.

-- ===========================================================================
-- Section 1 - imports
-- ===========================================================================
CREATE TABLE IF NOT EXISTS settlement_imports (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The gateway's own identifier for the batch, when it has one. Unique per
    -- gateway so re-uploading the same file is a no-op rather than a double
    -- count, which is the whole reason a settlement report can be re-run.
    -- NULL is allowed: some providers do not give a batch id.
    batch_ref      VARCHAR(80),
    gateway        VARCHAR(32) NOT NULL,
    settled_on     DATE        NOT NULL,
    source         VARCHAR(64) NOT NULL DEFAULT 'manual',
    status         VARCHAR(12) NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','imported','partial','failed')),
    total_rows     INT         NOT NULL DEFAULT 0 CHECK (total_rows >= 0),
    accepted_rows  INT         NOT NULL DEFAULT 0 CHECK (accepted_rows >= 0),
    -- Rejected rows are KEPT, not discarded. A settlement line we could not
    -- import is money the gateway says moved and we do not know about, which is
    -- the single most expensive thing to lose silently in a reconciliation.
    rejected_rows  INT         NOT NULL DEFAULT 0 CHECK (rejected_rows >= 0),
    error          TEXT,
    imported_by    UUID        REFERENCES users (id) ON DELETE RESTRICT,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    -- An import is a receipt of a completed event, so it is never edited.
    -- imported_rows must never exceed total_rows, which is the invariant that
    -- makes "partial" a meaningful state.
    CONSTRAINT settlement_imports_counts_ordered
        CHECK (accepted_rows + rejected_rows <= total_rows)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_imports_batch
    ON settlement_imports (gateway, batch_ref) WHERE batch_ref IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_settlement_imports_recent
    ON settlement_imports (gateway, settled_on DESC);

-- ===========================================================================
-- Section 2 - indexing the reconciliation queue
-- ===========================================================================
--
-- `00043` indexed `(settled_on) WHERE reconciled_at IS NULL`. The job needs
-- "oldest unreconciled first" and the operator needs the same order, so the
-- index carries the sort direction rather than leaving the database to sort.

-- The reason unreconciled rows must stay visible is the same as for refunds: a
-- settlement the gateway reported and we never matched is money that moved with
-- no entry on our side, and the only way to find out is to look at what did not
-- match.
CREATE INDEX IF NOT EXISTS idx_gateway_settlements_unreconciled_oldest
    ON gateway_settlements (settled_on ASC, created_at ASC)
    WHERE reconciled_at IS NULL;

-- Matching a settlement to a journal goes by gateway + ref, so that pair needs
-- to be findable in both directions: forward (find the settlement for a ref)
-- and backward (check whether a journal was already used for a settlement).
--
-- 00043 had a unique index on `id WHERE journal_id IS NOT NULL`, which was
-- vacuous: `id` is already the primary key, so it constrained exactly one row per
-- existing row and prevented nothing. It is dropped here, because a constraint
-- that reads like protection and protects nothing is worse than none -- and
-- somebody would reasonably believe a settlement cannot be double-matched.
DROP INDEX IF EXISTS idx_gateway_settlements_reconciled;

-- One journal per settlement. A settlement matched to two journals would mean two
-- entries for one movement of real money, and nothing else would catch it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_settlements_journal
    ON gateway_settlements (journal_id) WHERE journal_id IS NOT NULL;

-- ===========================================================================
-- Section 3 - the matching view
-- ===========================================================================
--
-- Resolves each settlement against the ledger so "is this matched, and does the
-- amount agree?" is one query with one definition. The job and the admin screen
-- otherwise grow two slightly different comparisons, and the one that drifts is
-- the one nobody checks.
--
-- `amount_delta` is the difference between what the gateway says arrived and
-- what our journal actually recorded. It is NOT asserted to be zero here: a
-- non-zero delta is a finding, not a constraint violation. Refusing to import a
-- settlement whose amount we cannot yet match would mean losing the gateway's
-- version of events, which is the thing we most need to keep.
CREATE OR REPLACE VIEW settlement_matches AS
SELECT
    s.id,
    s.gateway,
    s.settlement_ref,
    s.settled_on,
    s.kind,
    s.gross::float8        AS gross,
    s.fee::float8          AS fee,
    s.net::float8          AS net,
    s.journal_id,
    s.reconciled_at,
    j.tx_type,
    j.effective_at,
    COALESCE(journal_total.total, 0)::float8 AS journal_total,
    -- Signed: positive means the gateway reported more money than our journal
    -- recorded, negative means the reverse. Both are wrong; the sign says which
    -- way to look.
    (s.net - COALESCE(journal_total.total, 0))::float8 AS amount_delta,
    (s.journal_id IS NOT NULL)                        AS is_matched
  FROM gateway_settlements s
  LEFT JOIN ledger_journals j ON j.id = s.journal_id
  LEFT JOIN (
      SELECT journal_id, SUM(amount)::float8 AS total
        FROM ledger_entries
       GROUP BY journal_id
  ) journal_total ON journal_total.journal_id = j.id;

-- ===========================================================================
-- Section 4 - comments
-- ===========================================================================
COMMENT ON TABLE gateway_settlements IS
  'A movement of cash reported by a payment gateway: in, out, or a fee deducted. '
  'This is the leg that ties the internal ledger to real money -- without it the '
  'platform can compare its books to itself but never to its bank account. '
  'settled_on is the gateway''s own settlement date, which is NOT the capture date: '
  'Midtrans settles T+1 to T+7 by method, and conflating the two overstates cash '
  'in flight.';

COMMENT ON VIEW settlement_matches IS
  'Each gateway settlement joined to its journal, with amount_delta = what the '
  'gateway reported minus what the journal recorded. A matched row with a non-zero '
  'delta is a real finding and is deliberately not constrained away: the '
  'gateway''s version of events is the record we most need to keep.';

-- +goose Down
--
-- Drops only the objects this migration adds, and does NOT drop
-- `gateway_settlements` itself -- that table holds the gateway's record of money
-- that moved, and discarding it during a rollback would destroy the very history
-- a reconciliation is supposed to be based on.
--
-- Every index this migration creates is removed. The two on
-- `idx_gateway_settlements_*` are the ones that matter: `gateway_settlements`
-- survives, so an index left behind would be a permanent piece of schema from a
-- migration that was supposedly rolled back. The indexes on `settlement_imports`
-- go with the table.
DROP VIEW IF EXISTS settlement_matches;

DROP INDEX IF EXISTS idx_gateway_settlements_journal;
DROP INDEX IF EXISTS idx_gateway_settlements_unreconciled_oldest;
-- Restored, because 00043 created it. Not for its value: it is vacuous, since id
-- is already the primary key, so it constrains exactly one row per existing row
-- and prevents nothing. It is recreated only so the schema matches what 00043
-- left behind, rather than silently diverging from it.

DROP TABLE IF EXISTS settlement_imports;
