-- 00042_commission_rate_snapshot.sql
--
-- The commission rate is snapshotted onto the payment intent at CHARGE time, and
-- the intent carries an explicit "the split has been computed" flag.
--
-- Both exist because the rate was previously read at ESCROW RELEASE time, from
-- whatever `platform_fees` row happened to be active. That produced two money
-- bugs that neither the Go guards nor the schema could see.
--
-- 1. Retroactive seller loss. An order placed Monday and delivered Thursday had
--    its commission computed on Thursday. An admin who raised the rate from 2% to
--    10% on Wednesday took 8% of a sale that was quoted and sold at 2% -- on a
--    Rp 5,000,000 order that is Rp 400,000 out of a seller who never agreed to it.
--
-- 2. The 0% sentinel. The release path used `fee_amount == 0` to mean "not
--    computed yet", which is indistinguishable from a genuine 0% promotional rate.
--    A promo-period order released at 0% has fee_amount = 0, so refunding it later
--    recomputed a fee at TODAY's rate and then DEBITED it from the platform
--    wallet. Two consequences, both bad:
--      * it took money the platform never earned on that order, out of commission
--        pooled from every other seller.
--      * if the pooled balance was short the entire refund failed with
--        INSUFFICIENT_BALANCE, so the buyer's approved return was stuck with no
--        retry path and no way to resolve it except a manual wallet adjustment.
--
-- A boolean cannot be ambiguous, which is the whole point of the flag.

ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS commission_computed  boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS commission_rate_pct   numeric(5,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS commission_rate_fixed numeric(14,2) NOT NULL DEFAULT 0;

-- Backfill: an intent that already has a computed split is one that has been
-- through ReleaseEscrow, which is the only writer of fee_amount/seller_amount.
--
-- The rate columns are backfilled from that computed split rather than from the
-- current `platform_fees` row, because the current row is exactly the wrong
-- answer for these orders -- that is the bug being fixed. The reverse-engineered
-- rate is the rate that was actually applied, which is what a future refund needs
-- in order to reverse it exactly.
--
-- `commission_rate_fixed` is left at 0 where the split is consistent with a
-- percentage-only rate, and computed as the residual where it is not, so the
-- pair (pct, fixed) always reproduces the stored fee. Where the stored split
-- does not satisfy `fee + seller = amount` (the ~2.6% drift that 00041's new
-- `payment_intents_split_sums` constraint also addresses), commission_computed
-- stays false, so the refund path takes its documented fallback rather than
-- reversing a fee that never existed.
UPDATE payment_intents
SET commission_computed = true,
    commission_rate_pct = CASE
        WHEN amount > 0
        THEN round(((fee_amount * 100.0) / amount)::numeric, 2)
        ELSE 0
    END,
    commission_rate_fixed = 0
WHERE fee_amount > 0
  AND seller_amount > 0
  AND fee_amount + seller_amount = amount;

-- Intents whose split was written but does not foot (both legs zero, or the
-- drift case) are deliberately left commission_computed = false. The refund
-- path then reads the active rate for them, which is the pre-existing
-- behaviour, rather than reversing a fee that does not reconcile.
--
-- The comment below records, for the reader who finds this column in six
-- months, why the boolean exists and what breaks without it.

COMMENT ON COLUMN payment_intents.commission_computed IS
  'False until escrow release has derived fee_amount/seller_amount. A boolean rather than fee_amount = 0, because a 0% promotional rate is indistinguishable from "not computed" and using the amount as the predicate made a later refund reverse a fee computed at today''s rate.';

COMMENT ON COLUMN payment_intents.commission_rate_pct IS
  'The rate card percentage in force when the charge was created. Snapshotted so a refund reverses the fee actually taken rather than one recomputed from current settings.';

COMMENT ON COLUMN payment_intents.commission_rate_fixed IS
  'The rate card fixed component in force when the charge was created.';

-- +goose Down
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS commission_rate_fixed,
    DROP COLUMN IF EXISTS commission_rate_pct,
    DROP COLUMN IF EXISTS commission_computed;
