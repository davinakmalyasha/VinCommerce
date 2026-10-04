-- 00047: one live seller hold per order per kind
--
-- A hold makes the seller's money non-withdrawable, so taking the same hold twice
-- is not a harmless duplicate -- it holds the money twice and releases it once,
-- leaving the seller permanently short by one hold's amount.
--
-- That is not hypothetical. `HoldSellerFunds` runs in its own transaction, so two
-- concurrent claims on the same order (a return claim and a dispute, or a
-- double-submitted return form) each insert their own row. A SELECT-then-INSERT
-- guard in Go cannot close that window any more than it could in the refund path:
-- both callers read "none exists" and both write.
--
-- So it is a database guarantee, the same argument as 00045's replay index. The
-- application-level `ON CONFLICT ... DO NOTHING RETURNING` on top of it turns the
-- loser into a named outcome (`SELLER_HOLD_EXISTS`) that the caller treats as
-- success, because the money is already protected -- which is what it asked for.
--
-- PARTIAL on `released_at IS NULL`, and that is the whole point. A released hold
-- is history; the constraint is about money that is held RIGHT NOW. Without the
-- predicate, a seller who had a return resolved and later opened a second claim on
-- the same order would be permanently unable to hold that money again.
--
-- Also `order_id IS NOT NULL`: a `payout` reservation reserves against a payout,
-- not an order, and the existing `idx_seller_reservations_payout` already makes
-- that side unique.

-- +goose Up

CREATE UNIQUE INDEX IF NOT EXISTS idx_seller_reservations_live_order_kind
    ON seller_reservations (order_id, kind)
    WHERE order_id IS NOT NULL AND released_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_seller_reservations_live_order_kind;
