-- 00052: make a missing commission snapshot distinguishable from a 0% rate
--
-- 00042 added the rate snapshot as:
--
--     commission_rate_pct   numeric(5,2)  NOT NULL DEFAULT 0
--     commission_rate_fixed numeric(14,2) NOT NULL DEFAULT 0
--
-- `NOT NULL DEFAULT 0` made two different things indistinguishable. An intent
-- created before 00042 carries `(0, 0)` because the columns were ADDED with that
-- default; an intent whose rate card genuinely was 0% carries `(0, 0)` because
-- that is what the rate was. Both read the same.
--
-- Two sites resolve that ambiguity by treating `(0, 0)` as "not provided" and
-- substituting the ACTIVE rate. That is the safer of the two guesses -- the
-- alternative silently stops charging commission on every legacy intent -- but it
-- is still a guess, and it produces a real defect: if the active rate was 0% when
-- an order was placed and an admin raises it before the order is delivered, the
-- release reads "no snapshot" and charges the seller the NEW rate on a sale that
-- was priced at 0%. That is the same class of bug 00042 set out to fix, surviving
-- in the one case where the rate is zero.
--
-- This column removes the guess. It says what the other two cannot: whether a
-- rate was actually written for this intent.
--
-- WHY IT DEFAULTS TO false AND IS NOT BACKFILLED WHOLESALE
--
-- false means "treat this like an intent with no snapshot", i.e. exactly today's
-- behaviour. Opting existing rows into `true` would be guessing at which of them
-- had a real 0% rate, which is the problem being solved. Deploying this changes
-- the behaviour of NOTHING that already exists.
--
-- The one backfill below IS safe, because a non-zero rate cannot be the column
-- default: if either rate is non-zero, something deliberately wrote it.

-- +goose Up

ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS commission_snapshot_present boolean NOT NULL DEFAULT false;

-- Unambiguous rows: a non-zero rate could only have been written on purpose, so
-- those intents demonstrably have a snapshot. This changes no behaviour -- the
-- existing `(0, 0)` test already used those rates -- it just makes the new column
-- agree with what is already true.
UPDATE payment_intents
   SET commission_snapshot_present = true
 WHERE commission_snapshot_present = false
   AND (commission_rate_pct <> 0 OR commission_rate_fixed <> 0);

-- The ambiguous rows are deliberately left false. They are intents with `(0, 0)`
-- that have not been released: pre-00042 rows, and rows created while the active
-- rate was 0%. Telling those apart needs information that does not exist, so they
-- keep the current fallback rather than getting a guess baked in.

COMMENT ON COLUMN payment_intents.commission_snapshot_present IS
    'True when a commission rate was actually written for this intent, so a (0,0) rate means 0% rather than "absent". Defaults to false so pre-00042 intents keep the active-rate fallback.';

-- +goose Down

ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS commission_snapshot_present;
