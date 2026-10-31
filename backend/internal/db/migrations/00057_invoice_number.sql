-- +goose Up
-- Invoice numbers.
--
-- The invoice printed the order number in the "Invoice" position and there was
-- no invoice sequence at all. An order number is a commerce reference, not a
-- fiscal document number: it is generated before payment, it is reused in
-- support conversations, and nothing prevents two orders from being paid in a
-- different order than their numbers. A tax invoice needs its own gapless-ish
-- sequence.
--
-- The number is assigned LAZILY, on the first authorised invoice view, rather
-- than at checkout. Assigning at checkout burns a number on every cancelled
-- order; assigning on first view means an invoice that is never issued never
-- consumes one, which is the behaviour an operator reconciling a sequence can
-- defend.
CREATE SEQUENCE IF NOT EXISTS invoice_number_seq START WITH 1;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS invoice_number VARCHAR(40);

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_invoice_number
    ON orders (invoice_number) WHERE invoice_number IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_orders_invoice_number;
ALTER TABLE orders DROP COLUMN IF EXISTS invoice_number;
DROP SEQUENCE IF EXISTS invoice_number_seq;
