-- +goose Up
-- Carrier tracking events.
--
-- `shipments.tracking_number` was the whole story: a seller typed an AWB, the
-- buyer saw a string, and nothing ever asked the carrier where the parcel was.
-- `MarkShipmentDelivered` existed with a comment saying no caller could reach
-- it because there was no shipment webhook. This table is the record a webhook
-- (or a poll) writes, and the unique key is what makes a retried delivery a
-- no-op rather than a second identical event.
CREATE TABLE IF NOT EXISTS shipment_tracking_events (
    id             BIGSERIAL PRIMARY KEY,
    shipment_id    UUID NOT NULL REFERENCES shipments (id) ON DELETE CASCADE,
    -- The carrier's event id when it sends one, otherwise a hash of the
    -- (status, detail, occurred_at) triple. UNIQUE (shipment_id, event_key)
    -- is the duplicate guard: carriers retry aggressively.
    event_key      VARCHAR(96) NOT NULL,
    carrier_status VARCHAR(32) NOT NULL,
    detail         TEXT NOT NULL DEFAULT '',
    occurred_at    TIMESTAMPTZ,
    source         VARCHAR(12) NOT NULL CHECK (source IN ('webhook', 'poll')),
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_shipment_tracking_event
    ON shipment_tracking_events (shipment_id, event_key);
CREATE INDEX IF NOT EXISTS idx_shipment_tracking_recent
    ON shipment_tracking_events (shipment_id, received_at DESC);

-- +goose Down
DROP TABLE IF EXISTS shipment_tracking_events;
