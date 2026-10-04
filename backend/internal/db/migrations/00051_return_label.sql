-- 00051: a return label, which had nowhere to live
--
-- WHY THIS EXISTS
--
-- `SellerDecideReturn` emails the buyer, on approval, the words "Silakan kirim
-- barang kembali" -- please send the goods back. That is the platform instructing a
-- buyer to do something it gives them no way to do. There is no return label, no
-- return parcel, no RMA number, and `shipments.kind` has carried a 'return' value
-- since 00050 with nothing that ever writes it.
--
-- So a buyer acting on that email has to improvise: find a courier, guess the
-- seller's address from the order page, and hand over goods with no record that it
-- is coming back. When it arrives the seller has no way to match it to the approved
-- return, and the refund stays blocked on `RefundReturn`.
--
-- WHAT IS ADDED, AND WHAT IS DELIBERATELY NOT
--
-- Two columns on `return_requests`, pointing at a `kind='return'` parcel:
--
--   return_shipment_id  the parcel, so "where is my return" is answerable
--   return_label_url    the label, which is a POINTER and not durable state
--
-- `return_label_url` is expiring and signed in every real carrier, exactly as
-- `shipments.label_url` is. The durable record is `shipments.label_format` and
-- `shipments.label_created_at`, which the FK already reaches. Keeping a second copy
-- of a signed URL would be storing something that stops working and cannot be told
-- apart from one that does not.
--
-- NO new value on `return_requests.status`.
--
-- The vocabulary stays 'requested' 'approved' 'rejected' 'returned' 'refunded'
-- 'closed'. The journey -- handed over, in transit, missed delivery -- lives on the
-- PARCEL, in `shipments.status`, which already models all of it including the
-- recoverable 'exception'.
--
-- Adding 'in_transit' to the return row was the obvious move and it is the wrong one:
-- the journey would then exist in two tables, and the defect 00050 had to fix in the
-- order status -- a row claiming something its own contents contradict -- comes back
-- one level down. One place says where the parcel is. The return row says whether the
-- return was approved and whether the refund happened.
--
-- ON DELETE SET NULL, and why it is not the default
--
-- `shipments` is `ON DELETE CASCADE` from `orders`. A plain FK from
-- `return_requests.return_shipment_id` would therefore make deleting an ORDER fail
-- once a return parcel exists: the cascade wants to remove the shipment, and the FK
-- refuses. The failure would surface as a constraint error on order deletion, in a
-- table nobody was thinking about, caused by a shipment created weeks earlier.
--
-- SET NULL is the honest behaviour: the parcel is gone, so the return row no longer
-- claims a parcel, and `return_requests.status` is left to describe the return on its
-- own terms. The alternative -- RESTRICT -- would make a return parcel a permanent
-- obstacle to deleting an order, which is not something a shipping label should ever
-- be able to do.
--
-- THE RETURN ADDRESS, WHICH HAD NOWHERE TO LIVE EITHER
--
-- `stores` has no address column. `00006_marketplace.sql:1-16` created it with
-- name, slug, description, logo, banner, status and counters, and no location of any
-- kind -- so there is no seller pickup address in the database at all.
--
-- Which means a return label has no correct destination to be sent TO, and the two
-- available fallbacks are both wrong:
--
--   * reuse `orders.shipping_address` -- that is the BUYER's address, so a return
--     label addressed from it posts the returned goods straight back to the buyer at
--     the seller's expense, which is a second parcel of the same items.
--   * invent a default from the store's city -- an address nobody chose is an address
--     the parcel does not arrive at, and the refund stays blocked forever.
--
-- So the seller states it. `return_address` is JSONB because the rest of the platform
-- stores addresses that way (`orders.shipping_address`, `00004:36`) and inventing a
-- second address shape would need a migration to read either.
--
-- DEFAULT '{}' rather than NOT NULL with no default: an absent address and an empty
-- one are the same fact here, and the read path checks for emptiness either way. What
-- it must never be is a plausible-looking guess.

-- +goose Up

ALTER TABLE stores
    ADD COLUMN IF NOT EXISTS return_address JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS return_shipment_id UUID REFERENCES shipments (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS return_label_url TEXT;

-- One parcel per return request, and the reverse lookup from a parcel back to the
-- return it belongs to.
--
-- The UNIQUE is the real constraint: a second return parcel for the same return
-- means two boxes of returned goods arriving against one approved refund, and the
-- seller matching them by eye. The index is a plain one because UNIQUE already gives
-- lookup by value.
CREATE UNIQUE INDEX IF NOT EXISTS idx_returns_return_shipment
    ON return_requests (return_shipment_id)
    WHERE return_shipment_id IS NOT NULL;

-- The reverse direction: given a return parcel, which return does it satisfy? Every
-- carrier webhook resolves a parcel first and then needs this, and without the index
-- that is a sequential scan of every return on every delivery event.
CREATE INDEX IF NOT EXISTS idx_shipments_return_request
    ON return_requests (order_id, status)
    WHERE return_shipment_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_shipments_return_request;
DROP INDEX IF EXISTS idx_returns_return_shipment;

ALTER TABLE return_requests DROP COLUMN IF EXISTS return_label_url;
ALTER TABLE return_requests DROP COLUMN IF EXISTS return_shipment_id;

ALTER TABLE stores DROP COLUMN IF EXISTS return_address;