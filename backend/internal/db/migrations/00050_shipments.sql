-- 00050: shipments, so an order can be more than one parcel
--
-- WHY THIS EXISTS
--
-- `orders` has carried a single `tracking_number` and a single `carrier` since
-- 00004:3-4. One pair per order, which means one parcel per order. Two situations
-- cannot be expressed at all:
--
--   * SPLIT SHIPPING. One seller order leaving in two parcels -- different
--     warehouses, a heavy item going separately. The second parcel has nowhere to
--     record its tracking number, so it overwrites the first.
--
--   * PARTIAL SHIPMENT OF A LINE. `order_items` has `quantity` and no shipped
--     counter, so shipping 2 of 3 units is not representable. The only honest
--     option is to mark the whole line shipped, which tells the buyer three were
--     sent when two were, and tells the seller to ship three.
--
-- `orders.status` also has no `partially_shipped`, and the state machine in
-- domain/order.go:35-43 goes packed -> shipped directly, so there is no way to
-- say "some of it went".
--
-- WHAT IS NORMALISED HERE
--
-- `shipments` holds one row per parcel. `shipment_items` says what is IN each
-- parcel and how many. The old `orders.tracking_number`/`carrier` survive as
-- denormalised mirrors of the FIRST OUTBOUND parcel, because the seller order
-- list, the buyer's order page and the admin search all read them, and rewriting
-- those call sites is not this migration's business.
--
-- OVER-SHIPMENT
--
-- `order_items.shipped_quantity` gets a CHECK against its own `quantity`, so the
-- database refuses to record more units shipped than were bought. That CHECK is
-- the backstop, not the mechanism: it only fires when the ITEM row is updated, so
-- two concurrent shipments could each pass it while their shipment_items rows
-- overshoot. The mechanism is the conditional update
--
--     UPDATE order_items
--        SET shipped_quantity = shipped_quantity + $qty
--      WHERE id = $id AND shipped_quantity + $qty <= quantity
--
-- whose zero-rows-affected IS the refusal. Both are needed and they say different
-- things: the CHECK is the invariant, the predicate is the concurrency control.
--
-- NOT ENFORCED BY THE SCHEMA, and stated so it is not assumed:
--
-- The sum of shipment_items.quantity per order_item across all live shipments. A
-- CHECK cannot see other rows, and a UNIQUE cannot sum. It is maintained by the
-- same conditional update inside the same transaction, and by nothing else.
--
-- `carrier_shipment_id` is UNIQUE per carrier rather than globally, because two
-- carriers may legitimately hand out the same string.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. Shipped quantity per line
-- ---------------------------------------------------------------------------
--
-- DEFAULT 0 and NOT NULL so the 00004-era rows backfill without a rewrite and
-- without a NULL branch in every read.
ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS shipped_quantity INT NOT NULL DEFAULT 0;

-- The over-shipment backstop. `quantity` is on the same row, so this is a legal
-- single-row CHECK; the danger is that it reads as the only control when it is
-- the weaker of the two.
-- `IF EXISTS` is REQUIRED, not defensive tidiness.
--
-- On a fresh database this constraint has never existed, because 00050 is what adds
-- it. A bare `DROP CONSTRAINT` raises
--
--     ERROR: constraint "order_items_shipped_quantity_check" of relation
--            "order_items" does not exist
--
-- and takes the whole migration with it -- so 00050 could only ever be applied to a
-- database that had already been partially migrated by itself. Found by executing
-- 00001 -> 00051 against a scratch database rather than by reading it.
ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_shipped_quantity_check;
ALTER TABLE order_items
    ADD CONSTRAINT order_items_shipped_quantity_check
    CHECK (shipped_quantity >= 0 AND shipped_quantity <= quantity);

-- ---------------------------------------------------------------------------
-- 2. 'partially_shipped' on the order
-- ---------------------------------------------------------------------------
--
-- The CHECK is dropped by LOOKING IT UP rather than by guessing its name. An
-- unnamed inline CHECK gets an auto-generated name (`orders_status_check`), but
-- relying on that is a trap: `DROP CONSTRAINT IF EXISTS` would silently do
-- nothing on a rename, the new CHECK would be added under a different name, and
-- BOTH would remain -- with the old one still rejecting 'partially_shipped'. Every
-- write would fail with a constraint error pointing at 00004.
--
-- So the lookup raises if it finds nothing. A migration that cannot find what it
-- is replacing must stop, not proceed.
-- +goose StatementBegin
DO $$
DECLARE
    c_name TEXT;
BEGIN
    -- Identified by the COLUMN it covers, via con.conkey, not by the word
    -- "status" appearing in its definition text.
    --
    -- `LIKE '%status%'` matched BOTH `orders_status_check` AND
    -- `orders_payment_status_check` (00004:86-88 and 00004:94-95), and
    -- `SELECT ... INTO` keeps ONE arbitrary row of the two. Dropping the payment
    -- check instead of the status check would leave `orders_status_check` in place,
    -- and every write of 'partially_shipped' would then be rejected by the very
    -- constraint this migration was supposed to replace -- the failure the comment
    -- above this block says it refuses to risk.
    SELECT con.conname INTO c_name
      FROM pg_constraint con
      JOIN pg_class rel ON rel.oid = con.conrelid
      JOIN pg_attribute att
        ON att.attrelid = rel.oid AND att.attname = 'status' AND att.attnum > 0
     WHERE rel.relname = 'orders'
       AND con.contype = 'c'
       AND con.conkey = ARRAY[att.attnum];

    IF c_name IS NULL THEN
        -- One literal: RAISE takes a format, not a concatenation (see the
        -- `orders` block above).
        RAISE EXCEPTION
            'no CHECK constraint on orders.status found; 00004 must have been altered by something this migration does not know about. Refusing to guess -- add partially_shipped to whatever constraint is there.';
    END IF;

    EXECUTE format('ALTER TABLE orders DROP CONSTRAINT %I', c_name);
END
$$;
-- +goose StatementEnd

ALTER TABLE orders
    ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending', 'paid', 'packed', 'partially_shipped', 'shipped',
                       'delivered', 'completed', 'cancelled', 'return_requested',
                       'returned'));

-- The indexes that read "what is still waiting to ship" now have a state in the
-- middle. idx_orders_shipped_at (00040:369) is partial on status = 'shipped', so
-- it never sees a half-shipped order; that is fine for its purpose (due-delivery
-- sweeps) and this index covers the operator queue.
CREATE INDEX IF NOT EXISTS idx_orders_partially_shipped
    ON orders (seller_id, updated_at DESC)
    WHERE status = 'partially_shipped';

-- ---------------------------------------------------------------------------
-- 2b. 'partially_shipped' on the order LINE
-- ---------------------------------------------------------------------------
--
-- The order status is not the only thing that can be half-done. A line of
-- quantity 3 with 2 units in a parcel is `partially_shipped`, and the seller
-- packing view needs to say so -- otherwise the line reads as fully shipped, which
-- is the original problem one level down.
--
-- Same lookup-and-raise treatment as `orders.status` above, for the same reason:
-- `DROP CONSTRAINT IF EXISTS` on a guessed name is a silent no-op that leaves two
-- CHECKs fighting over the column.
-- +goose StatementBegin
DO $$
DECLARE
    c_name TEXT;
BEGIN
    -- Same reasoning as `orders` above: identified by conkey, not by text.
    SELECT con.conname INTO c_name
      FROM pg_constraint con
      JOIN pg_class rel ON rel.oid = con.conrelid
      JOIN pg_attribute att
        ON att.attrelid = rel.oid AND att.attname = 'status' AND att.attnum > 0
     WHERE rel.relname = 'order_items'
       AND con.contype = 'c'
       AND con.conkey = ARRAY[att.attnum];

    IF c_name IS NULL THEN
        -- One literal: RAISE takes a format, not a concatenation (see the
        -- `orders` block above).
        RAISE EXCEPTION
            'no CHECK constraint on order_items.status found; 00004 must have been altered by something this migration does not know about. Refusing to guess -- add partially_shipped to whatever constraint is there.';
    END IF;

    EXECUTE format('ALTER TABLE order_items DROP CONSTRAINT %I', c_name);
END
$$;
-- +goose StatementEnd

-- 'pending', 'paid', 'packed', 'partially_shipped', 'shipped', 'delivered',
-- 'cancelled', 'return_requested', 'returned'.
--
-- 'packed' IS here, and its absence was a bug. 00004:129-131 already allowed
-- 'packed' on the LINE, while this migration's first draft wrote a vocabulary without
-- it and asserted in a comment that 00004 had no 'packed' there. Both were wrong.
-- Omitting the value makes this ADD CONSTRAINT fail on any line still marked 'packed',
-- so on a real database the migration could not apply at all. Preserving the existing
-- vocabulary is the entire job of this statement; only 'partially_shipped' is new.
ALTER TABLE order_items
    ADD CONSTRAINT order_items_status_check
    CHECK (status IN ('pending', 'paid', 'packed', 'partially_shipped', 'shipped',
                       'delivered', 'cancelled', 'return_requested', 'returned'));

-- ---------------------------------------------------------------------------
-- 3. shipments
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS shipments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    seller_id           UUID NOT NULL REFERENCES users (id),

    -- Which parcel of this order. 1-based, and unique per (order, kind) so two
    -- parcels cannot both claim to be the first.
    sequence            INT NOT NULL CHECK (sequence > 0),

    -- 'outbound' is seller -> buyer. 'return' is buyer -> seller, and carries a
    -- RETURN LABEL rather than a shipping label. Kept in one table rather than two
    -- because a return is the same physical journey described the other way round,
    -- and the webhook path that updates it is identical.
    kind                VARCHAR(10) NOT NULL DEFAULT 'outbound'
                        CHECK (kind IN ('outbound', 'return')),

    -- 'ready'         label not bought yet
    -- 'label_created' label exists, parcel not handed over
    -- 'in_transit'    carrier has it
    -- 'delivered'
    -- 'exception'     carrier reported a problem; NOT terminal, and not a failure
    -- 'cancelled'     parcel abandoned before dispatch
    -- 'voided'        a bought label that will never be used
    --
    -- 'exception' being non-terminal is deliberate. A carrier that reports a
    -- delivery exception usually re-delivers, and collapsing that into a failure
    -- state would make a recoverable hiccup look like a lost parcel.
    status              VARCHAR(16) NOT NULL DEFAULT 'ready'
                        CHECK (status IN ('ready', 'label_created', 'in_transit',
                                          'delivered', 'exception', 'cancelled',
                                          'voided')),

    -- 'manual' is a first-class carrier, not a placeholder. COD deliveries and
    -- island deliveries routinely have no AWB at all, and forcing those through
    -- an integration would mean leaving the parcel unrecorded.
    carrier             VARCHAR(40) NOT NULL DEFAULT 'manual',
    carrier_service     VARCHAR(40),
    carrier_shipment_id VARCHAR(80),
    tracking_number     VARCHAR(64),

    -- The label. label_url is a signed, expiring URL in practice, so it is NOT
    -- treated as durable state and is not in the audit trail -- label_format and
    -- label_created_at are.
    label_format        VARCHAR(8) CHECK (label_format IN ('pdf', 'zpl')),
    label_url           TEXT,
    label_created_at    TIMESTAMPTZ,

    -- Denormalised from shipment_items for the carrier API and for rate shopping.
    -- Maintained by the service, and asserted against the items in tests: a
    -- weight the carrier was told that disagrees with the contents is a parcel
    -- billed wrong and delivered wrong.
    weight_grams        INT NOT NULL DEFAULT 0 CHECK (weight_grams >= 0),

    shipped_at          TIMESTAMPTZ,
    delivered_at        TIMESTAMPTZ,
    cancelled_at        TIMESTAMPTZ,

    -- Newest carrier webhook applied, so an out-of-order delivery can be
    -- recognised as stale and dropped rather than moving a parcel backwards.
    last_event_at       TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (order_id, kind, sequence)
);

-- A tracking number identifies a parcel within a carrier. Unique per carrier, not
-- globally: two different carriers may hand out the same digits.
CREATE UNIQUE INDEX IF NOT EXISTS idx_shipments_tracking
    ON shipments (carrier, tracking_number)
    WHERE tracking_number IS NOT NULL;

-- The idempotency key for carrier webhooks. Without it, a redelivered
-- "in_transit" after a "delivered" would walk the parcel backwards.
CREATE UNIQUE INDEX IF NOT EXISTS idx_shipments_carrier_ref
    ON shipments (carrier, carrier_shipment_id)
    WHERE carrier_shipment_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_shipments_order
    ON shipments (order_id, kind, sequence);
CREATE INDEX IF NOT EXISTS idx_shipments_seller_status
    ON shipments (seller_id, status, created_at DESC);
-- The dispatch sweep: parcels with a label that have not been handed over.
CREATE INDEX IF NOT EXISTS idx_shipments_awaiting_dispatch
    ON shipments (carrier, created_at)
    WHERE status = 'label_created';

-- ---------------------------------------------------------------------------
-- 4. shipment_items
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS shipment_items (
    shipment_id   UUID NOT NULL REFERENCES shipments (id) ON DELETE CASCADE,
    order_item_id UUID NOT NULL REFERENCES order_items (id) ON DELETE CASCADE,

    quantity      INT NOT NULL CHECK (quantity > 0),

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One row per line per parcel. This is what makes split shipping possible at
    -- all: the same order_item may appear in several parcels, each with part of
    -- its quantity.
    --
    -- It does NOT stop two parcels claiming the same units; only the sum across
    -- rows would, and a CHECK cannot see other rows.
    PRIMARY KEY (shipment_id, order_item_id)
);

-- "Which parcels contain this line", for the seller packing view.
CREATE INDEX IF NOT EXISTS idx_shipment_items_item
    ON shipment_items (order_item_id);

-- ---------------------------------------------------------------------------
-- 5. Backfill: existing tracking becomes a first outbound parcel
-- ---------------------------------------------------------------------------
--
-- The old model was one tracking pair per order, so the honest reading of that
-- data is "everything in this order went in one parcel". The items are attached
-- at FULL quantity, and shipped_quantity is set to match.
--
-- This reads existing data, so it is the one statement in this migration that
-- depends on there BEING existing rows. It is guarded, and it is written to be
-- re-runnable: ON CONFLICT DO NOTHING on (order_id, kind, sequence) means a
-- second execution cannot duplicate the parcels.
--
-- Orders in a shipped-or-later state with NO tracking number are included, with
-- carrier 'manual'. Those are real: COD and local deliveries frequently have no
-- AWB, and 00004 made tracking optional. Excluding them would invent parcels that
-- were never dispatched; including them keeps the queue honest.
-- GUARD THE WEIGHT FIRST.
--
-- `shipments.weight_grams` gets `CHECK (weight_grams >= 0)`, and the source column
-- `order_items.weight_grams` has no such guard -- 00041 added one to
-- `product_variants` and not to `order_items`. One historical line with a negative
-- weight therefore makes the SUM below negative, and the INSERT fails on the new
-- CHECK, rolling the migration back.
--
-- Clamping to 0 is the same choice 00041 makes for `product_variants`, and it errs
-- toward under-charging shipping rather than crediting it.
UPDATE order_items SET weight_grams = 0 WHERE weight_grams < 0;

INSERT INTO shipments (
    order_id, seller_id, sequence, kind, status, carrier, tracking_number,
    weight_grams, shipped_at, delivered_at, last_event_at
)
SELECT o.id,
       o.seller_id,
       1,
       'outbound',
       CASE
           WHEN o.delivered_at IS NOT NULL THEN 'delivered'
           WHEN o.shipped_at  IS NOT NULL THEN 'in_transit'
           ELSE 'ready'
       END,
       COALESCE(NULLIF(o.carrier, ''), 'manual'),
       NULLIF(o.tracking_number, ''),
       COALESCE(agg.grams, 0),
       o.shipped_at,
       o.delivered_at,
       -- No webhook ever arrived for a pre-00050 parcel, so last_event_at is NULL
       -- rather than backdated to shipped_at. NULL means "no carrier has spoken",
       -- which is the truth; a backdated value would make the first real webhook
       -- look stale and get dropped.
       NULL
  FROM orders o
  LEFT JOIN (
      SELECT order_id, SUM(weight_grams * quantity) AS grams
        FROM order_items GROUP BY order_id
  ) agg ON agg.order_id = o.id
 WHERE o.status IN ('partially_shipped', 'shipped', 'delivered', 'completed')
   -- `idx_shipments_tracking` is UNIQUE (carrier, tracking_number), and 00010 made
   -- `orders.tracking_number` free text that nothing validated -- so the same AWB can
   -- legitimately appear on two orders, by a seller typo or by pasting one order's
   -- number into another. `ON CONFLICT (order_id, kind, sequence)` does NOT cover that
   -- index, so without this predicate the whole migration aborts on a duplicate.
   --
   -- The loser keeps a NULL tracking number rather than being skipped: dropping the row
   -- would discard the order's only record that it shipped, and this migration is the
   -- only thing that will ever import that history.
   AND (o.tracking_number IS NULL
        OR NOT EXISTS (
            SELECT 1 FROM shipments x
             WHERE x.carrier = COALESCE(NULLIF(o.carrier, ''), 'manual')
               AND x.tracking_number = o.tracking_number
        ))
ON CONFLICT (order_id, kind, sequence) DO NOTHING;

-- Attach the backfilled parcels to their order's lines.
--
-- RESTRICTED TO sequence = 1.
--
-- The join was `oi.order_id = s.order_id` with no sequence filter, so on any re-run
-- every outbound parcel of an order received EVERY line at FULL quantity: a parcel 2
-- created after the migration came back containing the whole order a second time.
-- This is the re-runnability rule 00044 states for settlement imports, applied to the
-- one backfill here that is allowed to be re-run.
--
-- `sequence = 1` is also the only semantically correct reading: the pre-00050 model
-- was ONE parcel per order, so parcel 1 is the parcel that contained the order.
INSERT INTO shipment_items (shipment_id, order_item_id, quantity)
SELECT s.id, oi.id, oi.quantity
  FROM shipments s
  JOIN order_items oi ON oi.order_id = s.order_id
 WHERE s.kind = 'outbound'
   AND s.sequence = 1
   AND NOT EXISTS (
       SELECT 1 FROM shipment_items si WHERE si.shipment_id = s.id
   )
ON CONFLICT DO NOTHING;

-- And mark those lines as fully shipped, which is what the single-tracking model
-- always meant.
UPDATE order_items oi
   SET shipped_quantity = oi.quantity
  FROM shipments s
 WHERE s.order_id = oi.order_id
   AND s.kind = 'outbound'
   AND s.sequence = 1
   AND oi.shipped_quantity = 0
   AND EXISTS (
       SELECT 1 FROM shipment_items si
        WHERE si.shipment_id = s.id
          AND si.order_item_id = oi.id
          AND si.quantity = oi.quantity
   );

-- Mirror the first outbound parcel back onto the order, for the seller order list
-- and the admin search that still read the old columns.
UPDATE orders o
   SET carrier = s.carrier,
       tracking_number = s.tracking_number
  FROM shipments s
 WHERE s.order_id = o.id
   AND s.kind = 'outbound'
   AND s.sequence = 1
   AND o.tracking_number IS NULL;

-- +goose Down

-- The mirrored columns keep the values they had at the point of reversal. Their
-- pre-00050 meaning was "the only parcel", which is now ambiguous; restoring NULL
-- would lose data and restoring the value would be a guess. The rows are deleted
-- explicitly, which is the only part of the Down that is unambiguous.
-- The two DELETEs are REDUNDANT: `DROP TABLE IF EXISTS shipments` below removes both
-- tables and everything in them, because `shipment_items` is `ON DELETE CASCADE` from
-- `shipments`. They were kept from an earlier draft of this file.
--
-- They are also not re-runnable. `DELETE FROM` has no `IF EXISTS` form, so once the
-- tables are gone this statement raises
--
--     ERROR: relation "shipment_items" does not exist
--
-- and the rollback stops there. A Down section that works exactly once is not a Down
-- section. Found by rolling 00001 -> 00051 back newest-first against a scratch
-- database.
--
-- What the mirrored columns keep is their value at the point of reversal, which is
-- ambiguous in both directions: their pre-00050 meaning was "the only parcel", and
-- restoring NULL would lose the tracking a seller typed while restoring the value
-- would be a guess. The comment above this block already says that, and it stands.


UPDATE orders
   SET status = 'packed'
 WHERE status = 'partially_shipped';

DROP INDEX IF EXISTS idx_shipments_awaiting_dispatch;
DROP INDEX IF EXISTS idx_shipments_seller_status;
DROP INDEX IF EXISTS idx_shipments_order;
DROP INDEX IF EXISTS idx_shipments_carrier_ref;
DROP INDEX IF EXISTS idx_shipments_tracking;
DROP TABLE IF EXISTS shipment_items;
DROP TABLE IF EXISTS shipments;

DROP INDEX IF EXISTS idx_orders_partially_shipped;

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_status_check;
ALTER TABLE orders
    ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending', 'paid', 'packed', 'shipped', 'delivered',
                       'completed', 'cancelled', 'return_requested', 'returned'));

-- RESTORE order_items_status_check, which 00004:129-131 created and which the Up
-- replaced. Dropping it without re-adding it left the column UNCONSTRAINED, which is
-- worse than either the old or the new vocabulary: any status string at all becomes
-- valid, typos included.
--
-- 'packed' is in this list because 00004 had it on the LINE. `partially_shipped` rows
-- are reverted to 'packed' first so the restore itself cannot fail on them.
ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_shipped_quantity_check;

UPDATE order_items SET status = 'packed' WHERE status = 'partially_shipped';

ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_status_check;
ALTER TABLE order_items
    ADD CONSTRAINT order_items_status_check
    CHECK (status IN ('pending', 'paid', 'packed', 'shipped', 'delivered',
                       'cancelled', 'return_requested', 'returned'));

ALTER TABLE order_items DROP COLUMN IF EXISTS shipped_quantity;