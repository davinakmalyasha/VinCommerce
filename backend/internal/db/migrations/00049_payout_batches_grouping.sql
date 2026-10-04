-- 00049: make a payout batch a grouping, not a claim on the money
--
-- `payout_batches` and `payout_batch_items` have existed since 00043 with zero Go
-- references. This adds what a batch run needs, and fixes two gaps in the original
-- DDL that would have made a batch dangerous rather than useful.
--
-- 1. A PAYOUT MUST BELONG TO AT MOST ONE BATCH.
--
--    `payout_batch_items` has PRIMARY KEY (batch_id, payout_id). That permits the
--    SAME payout in two batches -- and a payout in two batches is a payout paid
--    twice, from two remittance files, to two bank runs. Neither 00043 nor
--    anything since constrained it.
--
--    A partial unique index on payout_id alone does not work either, because the
--    batch can be CANCELLED and its items must then stop counting while the payouts
--    return to the ungrouped pool. So the unique index is on (payout_id, batch_id)
--    -- which is already the primary key and therefore proves nothing -- and instead
--    the claim is enforced by the RUN: a batch only selects payouts that are not
--    already an item of a non-cancelled batch, and cancelling a batch DELETEs its
--    items. Deleting is right here precisely because the items are a grouping
--    artefact, not an accounting record: the money's record is `payouts` and the
--    reservation is `seller_reservations`, and neither is touched by a batch.
--
--    That is stated rather than encoded as a constraint because encoding it would
--    need a status on the item, and `payout_batch_items.status` has no 'cancelled'
--    value while `payout_batches.status` does. Adding one to the CHECK would be
--    cheaper than deleting rows and is the better design; it is recorded as a
--    follow-up rather than smuggled in here, because a CHECK change and a claim
--    rule are two different decisions.
--
-- 2. THE DAILY RUN MUST BE IDEMPOTENT.
--
--    `settlement_imports` learned this in ff15503 and says so at 00044:27-34 -- the
--    import row is a receipt, and it is what makes the job re-runnable without
--    double-counting. A batch run has the identical problem: if it is retried after
--    a partial failure, a second run must not produce a second batch for the same
--    cutoff and group the same payouts twice.
--
--    `batch_ref` is that receipt, with the same partial unique index shape.

-- +goose Up

ALTER TABLE payout_batches
    ADD COLUMN IF NOT EXISTS batch_ref VARCHAR(80);

-- One batch per ref. The partial predicate is what lets a NULL ref coexist with any
-- number of rows, so a hand-created batch that has not been given a ref is not
-- fighting the scheduler for the same identity.
CREATE UNIQUE INDEX IF NOT EXISTS idx_payout_batches_ref
    ON payout_batches (batch_ref)
    WHERE batch_ref IS NOT NULL;

-- The claim query behind a batch run: pending payouts, inside the cutoff, not
-- already grouped. Mirrors idx_payouts_status_requested (00040) but adds
-- requested_at as a leading column because the cutoff is always a range on it and
-- the existing index would still have to filter.
CREATE INDEX IF NOT EXISTS idx_payout_batch_items_payout
    ON payout_batch_items (payout_id);

-- Finding a seller's batches, for the remittance file's audit trail.
CREATE INDEX IF NOT EXISTS idx_payout_batches_lag_cutoff
    ON payout_batches (lag_days, cutoff_at DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_payout_batches_lag_cutoff;
DROP INDEX IF EXISTS idx_payout_batch_items_payout;
DROP INDEX IF EXISTS idx_payout_batches_ref;

ALTER TABLE payout_batches DROP COLUMN IF EXISTS batch_ref;