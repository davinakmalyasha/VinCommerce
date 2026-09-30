-- 00045: make refund replay a database guarantee
--
-- Replay protection for provider refund notifications was, until now, an
-- application-level convention: look the refund up by the provider's reference,
-- and if it is not there, record it. Two things can go wrong with that, and both
-- of them happened.
--
--   1. The lookup could be wrong. `refunds` had no unique index on the provider
--      reference, so nothing stopped two rows sharing one. The comment on
--      refunds even claimed "a refund is never issued twice against the same
--      gateway reference" while creating no constraint that said so.
--   2. The check and the write were separate statements. Even with a perfectly
--      correct lookup, two identical webhooks arriving concurrently both read
--      "not recorded" and both inserted. Midtrans retries for up to 24 hours and
--      does not serialise, so this is a race that a retry schedule can win.
--
-- This migration makes the first impossible and gives the service the tool for
-- the second: a unique index, and an INSERT ... ON CONFLICT that claims the
-- reference atomically rather than asking permission first.

-- +goose Up

-- Refuse to proceed if existing rows already collide.
--
-- NOT a de-duplication. Deleting a refund row to make an index build would delete
-- the record of money that left, and the zero-sum ledger trigger would then have
-- journals pointing at rows that no longer exist. Two rows sharing a provider
-- reference is a finding for a person -- it means a duplicate was already applied
-- and someone has to decide which one is real -- so this migration stops and says
-- so, naming the offending references.
--
-- In practice this should find nothing: `CreateRefund` bound a literal empty
-- string to gateway_ref until it was fixed, so every provider refund was recorded
-- with NULL, and NULLs do not collide in a unique index.
DO $$
DECLARE
    dup_count INT;
    example   TEXT;
BEGIN
    SELECT count(*) INTO dup_count
      FROM (
          SELECT 1
            FROM refunds
           WHERE gateway_ref IS NOT NULL
           GROUP BY gateway, gateway_ref
          HAVING count(*) > 1
      ) d;

    IF dup_count > 0 THEN
        SELECT gateway || '/' || gateway_ref INTO example
          FROM refunds
         WHERE gateway_ref IS NOT NULL
         GROUP BY gateway, gateway_ref
        HAVING count(*) > 1
         LIMIT 1;

        RAISE EXCEPTION
            'refunds already contains % duplicate provider reference(s), e.g. %. Reconcile them by hand before applying this migration: two rows sharing one gateway_ref means a provider refund was already applied twice, and choosing which to keep is an accounting decision, not a migration''s.',
            dup_count, example;
    END IF;
END $$;

-- The uniqueness the replay guard depends on.
--
-- Partial on `gateway_ref IS NOT NULL`, and deliberately so. A refund we initiate
-- has no provider reference until the provider assigns one, so those rows are NULL
-- and there will be many of them; making them unique on a column they do not have
-- would be meaningless. (NULLs are already distinct in a Postgres unique index, so
-- this is about the index being an index over the rows that matter, not about
-- avoiding a collision.)
--
-- (gateway, gateway_ref) rather than gateway_ref alone: two gateways may
-- legitimately issue the same refund id, and `providerRefundKey` is already scoped
-- per gateway for that reason.
CREATE UNIQUE INDEX idx_refunds_gateway_ref_unique
    ON refunds (gateway, gateway_ref)
    WHERE gateway_ref IS NOT NULL;

-- Lookups by provider reference now have an index to use. Before this, the
-- replay guard scanned every refund on the intent and compared the composed key in
-- Go -- which is why the guard was correct in principle and unusable in practice.
CREATE INDEX idx_refunds_replay_lookup
    ON refunds (payment_intent_id, gateway, gateway_ref)
    WHERE gateway_ref IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_refunds_replay_lookup;
DROP INDEX IF EXISTS idx_refunds_gateway_ref_unique;
