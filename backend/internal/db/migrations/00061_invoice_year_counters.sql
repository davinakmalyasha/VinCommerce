-- +goose Up
-- Invoice numbers reset per fiscal year.
--
-- The first implementation used a single global sequence and a YYYYMM prefix.
-- That never repeats, but an Indonesian tax sequence is per year, and an
-- operator reconciling a year's invoices should not have to filter by month.
--
-- A counter row per year, incremented under an advisory lock in the same
-- transaction as the order update. The advisory lock is deliberate: invoice
-- issuance is low volume, and a single serialization point removes the
-- read-then-write race without a retry loop. A sequence would have been
-- faster but cannot reset without DDL, and gaps from rolled-back assignments
-- are exactly what an auditor asks about.
CREATE TABLE IF NOT EXISTS invoice_counters (
    fiscal_year  INT PRIMARY KEY,
    last_number  INT NOT NULL DEFAULT 0 CHECK (last_number >= 0)
);

-- Carry forward anything already issued this year, so the new format cannot
-- re-issue a number that happens to collide with the old one in spirit.
INSERT INTO invoice_counters (fiscal_year, last_number)
SELECT EXTRACT(YEAR FROM now())::int,
       COALESCE(MAX((regexp_replace(invoice_number, '^INV-\d{4}-', ''))::int), 0)
  FROM orders
 WHERE invoice_number ~ ('^INV-' || EXTRACT(YEAR FROM now())::int || '-\d+$')
ON CONFLICT (fiscal_year) DO NOTHING;

-- The global sequence is replaced by the counter and would only be a second
-- source of truth.
DROP SEQUENCE IF EXISTS invoice_number_seq;

-- +goose Down
CREATE SEQUENCE IF NOT EXISTS invoice_number_seq START WITH 1;
DROP TABLE IF EXISTS invoice_counters;
