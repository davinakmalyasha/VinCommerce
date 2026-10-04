-- +goose Up
-- 00040: money-safety constraints + hot-path indexes.
--
-- Two things this migration does:
--
--  (a) Move money invariants out of Go and into the schema. Every one of
--      these previously lived only in application code, which means any new
--      code path that forgot a guard could mint or lose money.
--
--  (b) Add the missing indexes on the hot paths. The most significant is
--      orders(created_at): there was no index supporting a bare created_at
--      range, so every analytics query and every worker sweep was a full
--      sequential heap scan.
--
-- This migration runs at application startup, so it must be safe against a
-- database that already contains data violating a new constraint. Each
-- constraint below is therefore either (1) preceded by a repair step, or
-- (2) skipped with a NOTICE when the data cannot be repaired without
-- destroying financial records. Nothing here deletes a wallet transaction.

-- ===========================================================================
-- Section 1 — disputes: a dispute resolves exactly once, one live per order.
--
-- Resolve() was an unguarded UPDATE that discarded RowsAffected, so resolving
-- the same dispute repeatedly re-ran the money side of the decision.
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    -- Keep the oldest live dispute per order; close the rest. Closing is
    -- non-destructive: the history and the messages are preserved.
    SELECT count(*) INTO dupes FROM (
        SELECT order_id FROM disputes
        WHERE status IN ('open', 'under_review')
        GROUP BY order_id HAVING count(*) > 1
    ) d;
    IF dupes > 0 THEN
        UPDATE disputes SET status = 'closed', admin_note = COALESCE(admin_note, '')
            || ' [auto-closed by 00040: duplicate live dispute for this order]'
        WHERE status IN ('open', 'under_review')
          AND id NOT IN (
              SELECT DISTINCT ON (order_id) id FROM disputes
              WHERE status IN ('open', 'under_review')
              ORDER BY order_id, created_at
          );
        RAISE NOTICE '00040: closed % duplicate live dispute(s)', dupes;
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS uq_disputes_one_open_per_order
    ON disputes (order_id) WHERE status IN ('open', 'under_review');

-- ===========================================================================
-- Section 2 — return_requests: at most one live claim per order item.
--
-- Without this a buyer could file N identical claims on one item and be
-- auto-approved for each (RETURN_AUTO_APPROVE_MAX makes items <= Rp 50.000
-- automatic), then collect N refunds.
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT order_item_id FROM return_requests
        WHERE status NOT IN ('closed', 'refunded', 'rejected')
        GROUP BY order_item_id HAVING count(*) > 1
    ) d;
    IF dupes > 0 THEN
        UPDATE return_requests SET status = 'rejected',
               admin_note = COALESCE(admin_note, '')
                   || ' [auto-rejected by 00040: duplicate live claim for this order item]'
        WHERE status NOT IN ('closed', 'refunded', 'rejected')
          AND id NOT IN (
              SELECT DISTINCT ON (order_item_id) id FROM return_requests
              WHERE status NOT IN ('closed', 'refunded', 'rejected')
              ORDER BY order_item_id, created_at
          );
        RAISE NOTICE '00040: rejected % duplicate live return claim(s)', dupes;
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS uq_return_open_per_item
    ON return_requests (order_item_id)
    WHERE status NOT IN ('closed', 'refunded', 'rejected');

-- ===========================================================================
-- Section 3 — platform_fees: exactly one active commission row.
--
-- ActiveFeeTx disambiguated with ORDER BY updated_at DESC LIMIT 1, so two
-- concurrent admin saves could leave two active rows and orders would settle
-- at a non-deterministic rate. Extra rows are deactivated, not deleted: the
-- fee history is a pricing audit trail.
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) - 1 INTO dupes FROM platform_fees WHERE is_active;
    IF dupes > 0 THEN
        UPDATE platform_fees SET is_active = FALSE
        WHERE is_active AND id NOT IN (
            SELECT id FROM platform_fees WHERE is_active
            ORDER BY updated_at DESC, id DESC LIMIT 1
        );
        RAISE NOTICE '00040: deactivated % duplicate active platform fee row(s)', dupes;
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_fees_one_active
    ON platform_fees ((is_active)) WHERE is_active;

-- Clamp out-of-range commission config before adding the CHECK. The admin
-- endpoint accepted `fixed` straight from the request body with no validation,
-- so a negative fee was reachable and ReleaseEscrow would pay it out of the
-- platform's pooled commission.
-- +goose StatementBegin
DO $$
BEGIN
    UPDATE platform_fees SET pct = 50   WHERE pct > 50;
    UPDATE platform_fees SET pct = 0    WHERE pct < 0;
    UPDATE platform_fees SET fixed = 0  WHERE fixed < 0;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_fees_pct_range') THEN
        ALTER TABLE platform_fees ADD CONSTRAINT platform_fees_pct_range
            CHECK (pct >= 0 AND pct <= 50);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_fees_fixed_nonneg') THEN
        ALTER TABLE platform_fees ADD CONSTRAINT platform_fees_fixed_nonneg
            CHECK (fixed >= 0);
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 4 — one ledger row per business event.
--
-- Every double-credit defence today is an application-level guarded UPDATE
-- that a future code path can simply omit. The index is added only when the
-- existing data already satisfies it: violating rows are financial records and
-- must never be deleted to make a DDL statement succeed.
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM wallet_transactions
        WHERE ref_id IS NOT NULL
        GROUP BY wallet_id, ref_id, kind, reason HAVING count(*) > 1
    ) d;
    IF dupes = 0 THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_wallet_tx_business_event
            ON wallet_transactions (wallet_id, ref_id, kind, reason)
            WHERE ref_id IS NOT NULL;
    ELSE
        RAISE WARNING
            '00040: skipped uq_wallet_tx_business_event — % pre-existing duplicate ledger group(s) found. Reconcile the ledger before adding it.',
            dupes;
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 5 — misc uniqueness that application code assumed but never enforced
--
-- Every index here is created only when the existing data already satisfies
-- it. Each one is a bare CREATE UNIQUE INDEX in the first version, applied to
-- tables this file's own comments say accumulate duplicates — so a single
-- pre-existing duplicate aborted the whole migration, taking the other 40+
-- indexes and 8 CHECK constraints with it.
-- ===========================================================================

-- A referral bonus is granted at most once per user. POST /referral/redeem
-- had no rate limit and no one-referral check, and Add() is a plain INSERT,
-- so calling it in a loop granted unlimited points worth unlimited discount.
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM loyalty_ledger
        WHERE reason = 'referral_bonus'
        GROUP BY user_id HAVING count(*) > 1
    ) d;
    IF dupes = 0 THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_bonus_once
            ON loyalty_ledger (user_id, reason) WHERE reason = 'referral_bonus';
    ELSE
        RAISE WARNING
            '00040: skipped uq_referral_bonus_once — % user(s) hold more than one referral_bonus row. Deduplicate loyalty_ledger and re-run.', dupes;
    END IF;
END $$;
-- +goose StatementEnd

-- The repository's ON CONFLICT DO NOTHING was a no-op because there was no
-- conflict target other than the random PK, so duplicate watches accumulated
-- and each duplicate notified the user again.
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM back_in_stock_alerts
        GROUP BY user_id, variant_id HAVING count(*) > 1
    ) d;
    IF dupes = 0 THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_backinstock_user_variant
            ON back_in_stock_alerts (user_id, variant_id);
    ELSE
        RAISE WARNING
            '00040: skipped uq_backinstock_user_variant — % duplicate watch(es). Deduplicate back_in_stock_alerts and re-run.', dupes;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM addresses WHERE is_default
        GROUP BY user_id HAVING count(*) > 1
    ) d;
    IF dupes = 0 THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_addresses_one_default
            ON addresses (user_id) WHERE is_default;
    ELSE
        -- Repairable without losing data: keep the oldest default per user and
        -- clear the rest. addresses is a user preference, not a ledger.
        UPDATE addresses SET is_default = FALSE
        WHERE is_default AND id NOT IN (
            SELECT DISTINCT ON (user_id) id FROM addresses
            WHERE is_default ORDER BY user_id, created_at
        );
        RAISE NOTICE '00040: cleared duplicate default address flags';
        CREATE UNIQUE INDEX IF NOT EXISTS uq_addresses_one_default
            ON addresses (user_id) WHERE is_default;
    END IF;
END $$;
-- +goose StatementEnd

-- The floating chat widget created a new seller session on every click.
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM chat_sessions
        WHERE type = 'seller' AND status = 'open' AND order_id IS NOT NULL
        GROUP BY order_id HAVING count(*) > 1
    ) d;
    IF dupes = 0 THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_chat_sessions_open_seller
            ON chat_sessions (order_id) WHERE type = 'seller' AND status = 'open';
    ELSE
        RAISE WARNING
            '00040: skipped uq_chat_sessions_open_seller — % order(s) have more than one open seller chat. Close the extras and re-run.', dupes;
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 6 — order arithmetic tripwires
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    repaired BIGINT;
BEGIN
    UPDATE orders SET discount_amount = subtotal WHERE discount_amount > subtotal;
    UPDATE orders SET total_amount = 0 WHERE total_amount < 0;

    -- Clamp rather than fail: a total that cannot be trusted is still better
    -- than a failed migration on a live database. Announce it loudly, because
    -- silently rewriting order money is exactly the kind of thing an operator
    -- needs to know happened.
    GET DIAGNOSTICS repaired = ROW_COUNT;
    IF repaired > 0 THEN
        RAISE NOTICE '00040: clamped % order(s) with an out-of-range total or discount', repaired;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_total_nonneg') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_total_nonneg CHECK (total_amount >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_discount_bounded') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_discount_bounded
            CHECK (discount_amount >= 0 AND discount_amount <= subtotal);
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 6b — the commission split must reconcile.
--
-- The repair MUST run before the constraint: a single negative row would
-- otherwise fail the ADD CONSTRAINT instead of being repaired. (The first
-- version had these two the wrong way round.)
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    repaired BIGINT;
BEGIN
    UPDATE payment_intents
    SET fee_amount = 0, seller_amount = amount
    WHERE fee_amount < 0 OR seller_amount < 0;
    GET DIAGNOSTICS repaired = ROW_COUNT;
    IF repaired > 0 THEN
        RAISE NOTICE '00040: repaired % payment intent(s) with a negative commission split', repaired;
    END IF;

    -- The float64 money representation in Go could not guarantee this.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payment_intents_split_nonneg') THEN
        ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_split_nonneg
            CHECK (fee_amount >= 0 AND seller_amount >= 0);
    END IF;
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 7 — widen the payment_intent status CHECK for the new
-- disputed_split terminal state. The existing constraint is located by
-- definition rather than by name, because Postgres' auto-generated name is
-- not guaranteed to be payment_intents_status_check.
-- ===========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    cname TEXT;
BEGIN
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'payment_intents'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) ILIKE '%status%'
    LIMIT 1;

    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE payment_intents DROP CONSTRAINT %I', cname);
    END IF;

    ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_allowed
        CHECK (status IN (
            'initiated', 'authorized', 'captured', 'released',
            'refunded', 'partially_refunded', 'disputed_split',
            'failed', 'expired'
        ));
END $$;
-- +goose StatementEnd

-- ===========================================================================
-- Section 8 — unindexed foreign keys.
--
-- Deleting or re-keying a parent row had to sequential-scan the child table to
-- enforce the constraint, and several of these block a legitimate delete.
-- ===========================================================================
CREATE INDEX IF NOT EXISTS idx_returns_order        ON return_requests (order_id);
CREATE INDEX IF NOT EXISTS idx_returns_buyer        ON return_requests (buyer_id);
CREATE INDEX IF NOT EXISTS idx_disputes_order       ON disputes (order_id);
CREATE INDEX IF NOT EXISTS idx_dispute_messages     ON dispute_messages (dispute_id);
CREATE INDEX IF NOT EXISTS idx_tickets_order        ON support_tickets (order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_variant  ON order_items (variant_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user         ON product_reviews (user_id);
CREATE INDEX IF NOT EXISTS idx_product_qa_answered  ON product_qa (answered_by);
CREATE INDEX IF NOT EXISTS idx_review_replies_user  ON review_replies (user_id);

-- ===========================================================================
-- Section 9 — orders hot paths
-- ===========================================================================
-- orders is append-only with physically correlated created_at: the textbook
-- BRIN case, ~1% the size of a btree and just as good for range scans.
CREATE INDEX IF NOT EXISTS idx_orders_created_brin
    ON orders USING brin (created_at) WITH (pages_per_range = 64);
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders (created_at DESC);

-- Worker sweeps all filter status plus a timestamp column. Shipped orders are
-- never removed, so these degrade without bound.
CREATE INDEX IF NOT EXISTS idx_orders_shipped
    ON orders (shipped_at) WHERE status = 'shipped';
CREATE INDEX IF NOT EXISTS idx_orders_delivered
    ON orders (delivered_at) WHERE status = 'delivered';

-- ===========================================================================
-- Section 10 — catalog hot paths
-- ===========================================================================
-- users.roles is TEXT[] with no index, so every role check was a full table
-- scan. CountRoleHolders is the last-admin lockout guard.
CREATE INDEX IF NOT EXISTS idx_users_roles      ON users USING gin (roles);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users (created_at DESC);

-- Moderation queues. PendingCounts runs on every ops dashboard poll and two of
-- its six sub-counts were unindexed sequential scans.
CREATE INDEX IF NOT EXISTS idx_reviews_pending
    ON product_reviews (created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_kyc_pending
    ON seller_kyc (created_at) WHERE status = 'pending';

-- These queries have a matching WHERE index but must still sort, because the
-- sort key is not in it.
CREATE INDEX IF NOT EXISTS idx_products_seller_recent
    ON products (seller_id, created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_seller_published
    ON products (seller_id, published_at DESC, created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_bestsellers
    ON products (sold_count DESC, avg_rating DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_category_sold
    ON products (category_id, sold_count DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_active_updated
    ON products (updated_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_reviews_product_recent
    ON product_reviews (product_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_variants_product_active
    ON product_variants (product_id) WHERE is_active;

-- The facet filter used attributes->>'slug' = ANY($n). `->>` returns text and
-- no GIN operator class can serve a text comparison, so the existing
-- jsonb_path_ops index was dead weight for the only query that used it. The
-- default operator class DOES serve the containment form the query was
-- rewritten to use, so the old one is REPLACED, not supplemented — keeping
-- both would double the GIN maintenance cost on a table every product write
-- touches.
DROP INDEX IF EXISTS idx_products_attrs;
CREATE INDEX IF NOT EXISTS idx_products_attrs_ops
    ON products USING gin (attributes jsonb_ops);

-- product_views already has a btree on (created_at) from 00023. It is the
-- highest write rate in the schema — one row per PDP view — so a SECOND index
-- is pure write amplification on a table that is only ever read by time
-- window. Swap the btree for a BRIN instead of adding to it: BRIN is ~1% the
-- size, is a poor fit for the small-table case but ideal here because the
-- table is large, append-only and physically ordered by created_at.
DROP INDEX IF EXISTS idx_product_views_time;
CREATE INDEX IF NOT EXISTS idx_product_views_time_brin
    ON product_views USING brin (created_at) WITH (pages_per_range = 64);

-- ===========================================================================
-- Section 11 — analytics + audit date ranges
-- ===========================================================================
CREATE INDEX IF NOT EXISTS idx_audit_created
    ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wallet_tx_reason_created
    ON wallet_transactions (reason, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_coupon_usages_used_at
    ON coupon_usages (used_at);
CREATE INDEX IF NOT EXISTS idx_payment_intents_status_created
    ON payment_intents (status, created_at);
CREATE INDEX IF NOT EXISTS idx_payouts_status_requested
    ON payouts (status, requested_at);
CREATE INDEX IF NOT EXISTS idx_returns_seller_status_req
    ON return_requests (seller_id, status, requested_at DESC);

-- ===========================================================================
-- Section 12 — non-sargable lookups and remaining sort keys
-- ===========================================================================
-- `lower(code) = lower($1)` cannot use a plain UNIQUE btree on code, and this
-- is hit on EVERY checkout with a coupon and EVERY registration with a
-- referral code. Codes differing only in case collide here, so repair first.
-- +goose StatementBegin
DO $$
DECLARE
    dupes BIGINT;
BEGIN
    SELECT count(*) INTO dupes FROM (
        SELECT 1 FROM coupons GROUP BY upper(code) HAVING count(*) > 1
    ) d;
    IF dupes > 0 THEN
        -- Lower-casing can collide two distinct codes. Retiring the loser is
        -- destructive to a live promotion, so do NOT auto-resolve: refuse.
        RAISE EXCEPTION
            '00040: % coupon code(s) differ only by case and cannot be indexed on upper(code). Resolve them in the admin UI, then re-run.', dupes;
    END IF;
    CREATE UNIQUE INDEX IF NOT EXISTS uq_coupons_code_upper ON coupons (upper(code));
END $$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS idx_orders_seller_status_recent
    ON orders (seller_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_store_follows_user_recent
    ON store_follows (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_refresh_sessions_active
    ON refresh_sessions (user_id, last_used_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_price_alerts_user_recent
    ON price_alerts (user_id, created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_backinstock_user_recent
    ON back_in_stock_alerts (user_id, created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_loyalty_user_recent
    ON loyalty_ledger (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_low_stock_alerts_variant
    ON low_stock_alerts (variant_id, sent_at DESC);

-- The low-stock sweep scanned every active variant every 30 minutes, and the
-- NOT EXISTS dedupe had no index to probe.
CREATE INDEX IF NOT EXISTS idx_variants_low_stock
    ON product_variants (stock, product_id) WHERE is_active;

-- Abandoned-cart recovery reads carts by updated_at for a partial predicate.
CREATE INDEX IF NOT EXISTS idx_carts_recoverable
    ON carts (updated_at)
    WHERE status = 'active' AND user_id IS NOT NULL AND recovery_sent_at IS NULL;

-- +goose Down
-- Indexes are pure additive and are dropped in reverse order. The two
-- replacements (products attributes, product_views time) are RESTORED, because
-- a Down that leaves the schema worse than the Up found it is not a Down.
CREATE INDEX IF NOT EXISTS idx_products_attrs
    ON products USING gin (attributes jsonb_path_ops);
DROP INDEX IF EXISTS idx_products_attrs_ops;
DROP INDEX IF EXISTS idx_product_views_time_brin;
CREATE INDEX IF NOT EXISTS idx_product_views_time ON product_views (created_at);
DROP INDEX IF EXISTS idx_carts_recoverable;
DROP INDEX IF EXISTS idx_variants_low_stock;
DROP INDEX IF EXISTS idx_low_stock_alerts_variant;
DROP INDEX IF EXISTS idx_loyalty_user_recent;
DROP INDEX IF EXISTS idx_backinstock_user_recent;
DROP INDEX IF EXISTS idx_price_alerts_user_recent;
DROP INDEX IF EXISTS idx_refresh_sessions_active;
DROP INDEX IF EXISTS idx_store_follows_user_recent;
DROP INDEX IF EXISTS idx_orders_seller_status_recent;
DROP INDEX IF EXISTS uq_coupons_code_upper;
DROP INDEX IF EXISTS idx_payouts_status_requested;
DROP INDEX IF EXISTS idx_returns_seller_status_req;
DROP INDEX IF EXISTS idx_payment_intents_status_created;
DROP INDEX IF EXISTS idx_coupon_usages_used_at;
DROP INDEX IF EXISTS idx_wallet_tx_reason_created;
DROP INDEX IF EXISTS idx_audit_created;
DROP INDEX IF EXISTS idx_kyc_pending;
DROP INDEX IF EXISTS idx_reviews_pending;
DROP INDEX IF EXISTS idx_users_created_at;
DROP INDEX IF EXISTS idx_users_roles;
DROP INDEX IF EXISTS idx_variants_product_active;
DROP INDEX IF EXISTS idx_products_attrs_ops;
DROP INDEX IF EXISTS idx_product_views_time_brin;
DROP INDEX IF EXISTS idx_products_active_updated;
DROP INDEX IF EXISTS idx_products_category_sold;
DROP INDEX IF EXISTS idx_products_bestsellers;
DROP INDEX IF EXISTS idx_products_seller_published;
DROP INDEX IF EXISTS idx_products_seller_recent;
DROP INDEX IF EXISTS idx_reviews_product_recent;
DROP INDEX IF EXISTS idx_review_replies_user;
DROP INDEX IF EXISTS idx_product_qa_answered;
DROP INDEX IF EXISTS idx_reviews_user;
DROP INDEX IF EXISTS idx_order_items_variant;
DROP INDEX IF EXISTS idx_tickets_order;
DROP INDEX IF EXISTS idx_dispute_messages;
DROP INDEX IF EXISTS idx_disputes_order;
DROP INDEX IF EXISTS idx_returns_buyer;
DROP INDEX IF EXISTS idx_returns_order;
DROP INDEX IF EXISTS idx_orders_delivered;
DROP INDEX IF EXISTS idx_orders_shipped;
DROP INDEX IF EXISTS idx_orders_created_at;
DROP INDEX IF EXISTS idx_orders_created_brin;

-- Undo the dedup repairs by reopening whatever this migration closed, so a
-- rollback does not silently hide disputes.
--
-- ORDER MATTERS: the reopening UPDATEs must run AFTER the partial unique
-- indexes are dropped, or they violate the very constraint being undone — the
-- Down then fails whenever the Up actually did any work, which is exactly when
-- a rollback is most likely to be attempted.
DROP INDEX IF EXISTS uq_chat_sessions_open_seller;
DROP INDEX IF EXISTS uq_addresses_one_default;
DROP INDEX IF EXISTS uq_backinstock_user_variant;
DROP INDEX IF EXISTS uq_referral_bonus_once;
DROP INDEX IF EXISTS uq_wallet_tx_business_event;
DROP INDEX IF EXISTS uq_platform_fees_one_active;
DROP INDEX IF EXISTS uq_return_open_per_item;
DROP INDEX IF EXISTS uq_disputes_one_open_per_order;

UPDATE disputes SET status = 'open'
    WHERE admin_note LIKE '%[auto-closed by 00040%';
UPDATE return_requests SET status = 'requested'
    WHERE admin_note LIKE '%[auto-rejected by 00040%';

-- Restore the original status CHECK. Any intent already in `disputed_split`
-- (written by DisputeSplitCredit while this migration was live) would fail
-- this, so those rows are moved to `refunded` first: a split settlement did
-- move money to the buyer, and `refunded` is the closest state the old CHECK
-- can represent. The order events and the wallet ledger still record exactly
-- what happened.
UPDATE payment_intents SET status = 'refunded' WHERE status = 'disputed_split';

ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_status_allowed;
ALTER TABLE payment_intents
    ADD CONSTRAINT payment_intents_status_check
    CHECK (status IN (
        'initiated', 'authorized', 'captured', 'released',
        'refunded', 'partially_refunded', 'failed', 'expired'
    ));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_split_nonneg;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_discount_bounded;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_total_nonneg;
ALTER TABLE platform_fees DROP CONSTRAINT IF EXISTS platform_fees_fixed_nonneg;
ALTER TABLE platform_fees DROP CONSTRAINT IF EXISTS platform_fees_pct_range;
