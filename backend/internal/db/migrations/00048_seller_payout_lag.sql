-- 00048: a per-seller payout lag
--
-- The platform default is configurable (`PAYOUT_LAG_DAYS`) and has been since
-- 00046's successor, but every seller got it. A marketplace needs both ends: a
-- seller whose cash cycle is monthly, and one who needs longer than seven days of
-- cover.
--
-- NULLABLE, and NULL means "use the platform default". That is the pattern
-- `stores.free_shipping_threshold` established (00014:33) and it is the right one
-- here for a reason beyond consistency: a DEFAULT value on the column would mean
-- "seven" forever, and the platform default is meant to change. NULL distinguishes
-- "has not chosen" from "chose the same thing as everyone", which is the
-- distinction that lets a future default change apply to everyone who never
-- chose.
--
-- The CHECK is on the value rather than on the column being non-null, because a
-- lag of 0 would release a hold the moment the order completed -- before any return
-- could be filed, making the hold decorative -- and a lag of zero must therefore
-- be indistinguishable from unset rather than a storable value. The upper bound
-- matches the service's `MaxPayoutLagDays`: past a quarter this is not a payment
-- term, it is a balance the seller cannot withdraw, and the complaint it generates
-- is indistinguishable from a platform that has lost the money.
--
-- Those bounds are duplicated from the service because this file cannot import Go,
-- and `TestStorePayoutLagBoundsMatchTheService` compares them.

-- +goose Up

ALTER TABLE stores
    ADD COLUMN IF NOT EXISTS payout_lag_days INT,
    ADD CONSTRAINT stores_payout_lag_days_bounded
        CHECK (payout_lag_days IS NULL
               OR payout_lag_days BETWEEN 1 AND 90);

-- +goose Down

ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_payout_lag_days_bounded;
ALTER TABLE stores DROP COLUMN IF EXISTS payout_lag_days;