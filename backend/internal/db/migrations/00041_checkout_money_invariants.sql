-- 00041_checkout_money_invariants.sql
--
-- The constraints that internal/service/money_property_test.go proves the
-- application now maintains, moved into the schema so a new code path that
-- forgets one fails at the INSERT rather than in production.
--
-- Migration 00040 moved eight money invariants from Go into CHECK constraints
-- for exactly this reason. These four are the remaining ones, and each one
-- corresponds to a defect that shipped.
--
-- Naming: 2026-09-29, continuing the sequence from 00040. See the note at the
-- bottom on why the numeric scheme is being retired.

-- ===========================================================================
-- Section 1 - commission legs must sum to the amount charged
-- ===========================================================================
--
-- `payment_intents.fee_amount + payment_intents.seller_amount` is the money the
-- buyer paid, split between the platform and the seller. 00040 added
-- `payment_intents_split_nonneg`, which asserts both legs are >= 0 -- and that
-- is all it did. Because both legs are rounded INDEPENDENTLY by Postgres when
-- written to NUMERIC(14,2) columns, a sweep of 200,000 (amount, pct, fixed)
-- triples found 5,116 mismatches (~2.6% of orders) where the two legs summed to
-- one sen more or less than the charge. Nobody could see it: no code compared
-- them, and the platform's own "commission earned" analytics sums gross
-- credits and never nets the refund debits.
--
-- The application now derives one leg and computes the other as the residual
-- AFTER rounding the first, so the identity holds by construction. This
-- constraint is what makes it hold for every future code path.
--
-- The condition `fee_amount > 0 OR seller_amount > 0` skips intents that have
-- not been through escrow release yet (both legs still 0), which is the normal
-- state for a `pending` or `initiated` intent.
-- REPAIR FIRST, THEN CONSTRAIN.
--
-- This file's own header says the split is made to hold "AFTER rounding the first",
-- which implies a repair pass. There was none, and the constraint was added with
-- `ADD CONSTRAINT ... CHECK`, which validates every existing row immediately.
--
-- A sweep of 200,000 (amount, pct, fixed) triples in this file's own comments
-- (lines 20-26) found 5,116 rows -- about 2.6% -- whose two legs summed to one sen
-- more or less than the charge, because `repository.Commission` rounded NEITHER leg
-- and Postgres rounds each NUMERIC independently. On a real database this ADD
-- CONSTRAINT raises check_violation and the whole migration rolls back, so the
-- invariant is never installed at all.
--
-- The repair takes the FEE as authoritative and derives the seller leg as the
-- residual, which is the convention the Go side is being fixed to match. The fee is
-- what the platform actually took, so it is the leg that must not move.
UPDATE payment_intents
   SET seller_amount = amount - fee_amount
 WHERE fee_amount > 0
   AND seller_amount <> amount - fee_amount;

ALTER TABLE payment_intents
    DROP CONSTRAINT IF EXISTS payment_intents_split_sums;

-- NOT VALID, then VALIDATE.
--
-- `NOT VALID` installs the constraint for every future write without the immediate
-- full-table scan, and `VALIDATE CONSTRAINT` then checks the historical rows while
-- taking only a SHARE UPDATE EXCLUSIVE lock -- so writes keep working and a failure
-- is still reported. Adding a validated CHECK directly takes ACCESS EXCLUSIVE for the
-- duration of the scan, which on `payment_intents` is a table the checkout writes on
-- every single order.
ALTER TABLE payment_intents
    ADD CONSTRAINT payment_intents_split_sums
    CHECK (
        (fee_amount = 0 AND seller_amount = 0)
        OR fee_amount + seller_amount = amount
    ) NOT VALID;

ALTER TABLE payment_intents
    VALIDATE CONSTRAINT payment_intents_split_sums;

-- ===========================================================================
-- Section 2 - a product variant cannot be created with a negative price or
--              a negative weight
-- ===========================================================================
--
-- `UpdateProduct` did not re-check `Price > 0`, unlike `CreateProduct`, so a
-- seller could create a product at a valid price and then update it to
-- -5000. `product_variants.price` has no CHECK, so the UPDATE succeeded and the
-- negative price became the line subtotal.
--
-- `weight_grams` was worse, because it reaches a money formula:
-- `weightKg += (WeightGrams * Quantity) / 1000`. There is no CHECK on the
-- column and no validation in either create or update, so `weight_grams: -5000`
-- produced a NEGATIVE shipping fee, which 00040's `orders_total_nonneg` then
-- turned into a checkout 500 for the buyer. CSV import wrote 0 when the column
-- was absent, so a seller who never thought about weight was shipping with no
-- weight at all.
--
-- A zero weight is still allowed and is not a bug: 0 is a legitimate value for a
-- seller who has not measured, and `billableKg` treats a non-positive total as
-- 0kg rather than negative. The constraint is on the SIGN, not on the value.
ALTER TABLE product_variants
    DROP CONSTRAINT IF EXISTS product_variants_price_nonneg;
ALTER TABLE product_variants
    DROP CONSTRAINT IF EXISTS product_variants_weight_nonneg;

-- REPAIR BEFORE THE CONSTRAINT, which is the whole point of having a repair.
--
-- The two statements were in this order: ADD CONSTRAINT (lines 71-74), then the
-- repairing UPDATEs (81-82). `ADD CONSTRAINT ... CHECK` validates every existing row
-- as it is added, so a table containing one negative `weight_grams` -- which is
-- reachable from a seller form and from a CSV with a stray hyphen -- raised
-- check_violation and rolled the migration back, and the repair below it never ran.
--
-- That is the exact inversion 00040's header calls out as a bug it once had, and it
-- was reintroduced here one file later.
--
-- The repair goes first, so by the time the constraint is validated there is nothing
-- left to reject. A negative weight is the more likely of the two and it produces a
-- NEGATIVE shipping fee via
-- `weightKg += (WeightGrams * Quantity) / 1000`, so clamping to 0 makes the order
-- priceable again and errs toward charging shipping rather than crediting it.
UPDATE product_variants SET weight_grams = 0 WHERE weight_grams < 0;
UPDATE product_variants SET price = 0        WHERE price < 0;

ALTER TABLE product_variants
    ADD CONSTRAINT product_variants_price_nonneg  CHECK (price >= 0);
ALTER TABLE product_variants
    ADD CONSTRAINT product_variants_weight_nonneg CHECK (weight_grams >= 0);

-- ===========================================================================
-- Section 3 - an order's money must foot to its own components
-- ===========================================================================
--
-- total_amount is computed as
--
--     subtotal - discount_amount + shipping_fee + insurance_fee
--
-- and the invoice renderer prints Subtotal, Diskon, Ongkir and TOTAL. It did not
-- print the insurance line, and it read `insurance_fee` from a field that was
-- never selected on any read path (00040's own projection lists omitted it), so
-- every insured order shipped an invoice whose TOTAL was higher than the sum of
-- its own printed lines, by exactly the fee the buyer was charged.
--
-- The projection is now a single shared constant, so the read path cannot drift
-- again. This constraint is the backstop for the arithmetic itself.
-- REPAIR FIRST, AGAIN.
--
-- Added with no repair and no NOT VALID, so any order whose `total_amount` does not
-- foot to its own components aborts the migration. And there is a documented source of
-- exactly that: the projection used to omit `insurance_fee`, so the arithmetic that
-- wrote `total_amount` was computed without a term the constraint then requires. Any
-- insured order created while that projection was live is off by the insurance fee.
--
-- The repair recomputes the total from its components, which is the identity the
-- invoice already claims to satisfy.
UPDATE orders
   SET total_amount = subtotal - discount_amount + shipping_fee + COALESCE(insurance_fee, 0)
 WHERE total_amount <> subtotal - discount_amount + shipping_fee + COALESCE(insurance_fee, 0);

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_money_foots;

-- NOT VALID then VALIDATE: `orders` is written on every checkout, and a direct
-- validated ADD takes ACCESS EXCLUSIVE for the length of the scan.
ALTER TABLE orders
    ADD CONSTRAINT orders_money_foots
    CHECK (
        total_amount = subtotal - discount_amount + shipping_fee + COALESCE(insurance_fee, 0)
    ) NOT VALID;

ALTER TABLE orders
    VALIDATE CONSTRAINT orders_money_foots;

-- ===========================================================================
-- Section 4 - a per-seller discount cannot exceed that seller's merchandise
-- ===========================================================================
--
-- `allocateGlobal` used to round each non-final share to the nearest rupiah,
-- accumulate, and give the LAST bundle `amount - assigned`. With 16 sellers
-- (15 bundles of Rp10 and one of Rp1) and a Rp1 coupon, fifteen shares each
-- rounded up to Rp0.07, `assigned` reached Rp1.05 and the last share was
-- Rp-0.05.
--
-- 00040 already added `orders_discount_bounded`, which is what turned that from
-- a silent mispricing into a hard 500 on the whole multi-seller checkout. This
-- does not add a new invariant; it names the constraint, because 00040 created
-- it inside a larger file and the name is the thing a future migration or a
-- reviewer needs in order to reason about it.
--
-- IF NOT EXISTS semantics: a DO block that only creates the constraint when it
-- is absent, so this migration is re-runnable.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'orders_discount_bounded'
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT orders_discount_bounded
            CHECK (discount_amount >= 0 AND discount_amount <= subtotal);
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 5 - whole-rupiah money
-- ===========================================================================
--
-- Not a constraint, a comment with teeth elsewhere in this change: IDR has no
-- sen. The columns are NUMERIC(14,2) because that is what existed when the
-- schema was written, and every money value the application writes is now a
-- whole rupiah.
--
-- The columns are deliberately NOT altered to NUMERIC(14,0) here. That change
-- is safe and mechanical, but it rewrites every money table, and doing it in
-- the same migration as four constraint changes makes a failure much harder to
-- attribute. It is a separate, single-purpose migration.
--
-- Why it matters, and why the constraint is not `amount = round(amount)`: a
-- CHECK of that form would fail on any row that already carries a sen component
-- from before this change, so adding it as a NOT VALID + VALIDATE migration
-- would be the correct pattern. It is not added here because the application no
-- longer produces fractional values, and the residual risk -- a hand-written
-- UPDATE or a future non-Go writer -- is better closed by the column type than
-- by a constraint the ORM has to know about.

-- ===========================================================================
-- Summary
-- ===========================================================================
-- Added:
--   payment_intents_split_sums      commission legs must equal the charge
--   product_variants_price_nonneg   a variant cannot have a negative price
--   product_variants_weight_nonneg  a variant cannot have a negative weight
--   orders_money_foots              the total must equal its own components
--   orders_discount_bounded         (named; created by 00040)
--
-- Each is asserted by a property test in internal/service/money_property_test.go.
-- The Go guards remain -- they produce good error messages instead of a raw
-- Postgres error -- but the schema is now the thing that cannot be bypassed.

-- ===========================================================================
-- Down
-- ===========================================================================
--
-- Symmetric with the Up above, in reverse. Notes on two of these:
--
-- orders_discount_bounded is NOT dropped here even though the Up re-adds it
-- defensively. 00040 owns that constraint: its Up creates it in a DO block and
-- its Down drops it. Rolling back 00041 and then 00040 leaves it gone, which is
-- 00040's Up to restore on the way back up. Dropping it here as well would make
-- the two migrations fight over the same object.
--
-- The two product_variants UPDATEs are not reversed. They clamp values that no
-- correct writer can produce, so there is nothing meaningful to restore and
-- inventing a "previous" value would be a guess.
-- +goose Down
--
-- The two money constraints are dropped rather than restored to a weaker form: they
-- did not exist before 00041, and re-adding a `NOT VALID` version would preserve an
-- invariant nobody has validated. `product_variants_price_nonneg` and
-- `product_variants_weight_nonneg` are likewise dropped, and the repair UPDATEs above
-- are NOT reversed -- clamping a negative price to 0 is the correct value, and
-- restoring -5000 would reintroduce the negative-shipping-fee defect on purpose.

-- Declared, not silently skipped: scripts/check-migration.mjs reads the marker
-- below and understands that this constraint belongs to another migration. With
-- it, the checker reports 00041 honestly. Without it, it forces a choice between
-- a redundant DROP and a false failure, and a guard that has to be worked around
-- is a guard people disable.
-- goose-down: not-owned orders_discount_bounded

ALTER TABLE orders        DROP CONSTRAINT IF EXISTS orders_money_foots;
ALTER TABLE product_variants DROP CONSTRAINT IF EXISTS product_variants_weight_nonneg;
ALTER TABLE product_variants DROP CONSTRAINT IF EXISTS product_variants_price_nonneg;
ALTER TABLE payment_intents  DROP CONSTRAINT IF EXISTS payment_intents_split_sums;
