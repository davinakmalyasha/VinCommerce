-- +goose Up
-- Whole-rupiah guards on the transactional money tables.
--
-- P0-4 is that money is `float64` end to end while the database is
-- NUMERIC(14,2). Migrating the Go side to integer minor units is a large
-- refactor; this migration closes the part that can be enforced today: a value
-- with a fractional rupiah must not be storable at all.
--
-- `col = trunc(col)` is not a rounding rule -- it REJECTS. The application
-- already rounds at its boundaries (RoundIDR, the whole-rupiah Midtrans
-- conversion, the allocation remainder split), so any row that violates this
-- is a path that forgot to, and the insert failing is the alarm. Silently
-- rounding in the database would hide exactly the defect the check exists to
-- find.
--
-- Deliberately NOT applied to catalogue prices: a seller price is an input,
-- not a money movement, and it is validated in the service where the error can
-- be a 400 rather than a constraint name. Order items are included because by
-- the time a line is written, the price is a charge.
ALTER TABLE orders
    ADD CONSTRAINT orders_money_whole_rupiah CHECK (
        subtotal = trunc(subtotal) AND
        discount_amount = trunc(discount_amount) AND
        shipping_fee = trunc(shipping_fee) AND
        insurance_fee = trunc(insurance_fee) AND
        total_amount = trunc(total_amount));

ALTER TABLE order_items
    ADD CONSTRAINT order_items_money_whole_rupiah CHECK (
        unit_price = trunc(unit_price) AND total = trunc(total));

ALTER TABLE payment_intents
    ADD CONSTRAINT payment_intents_money_whole_rupiah CHECK (
        amount = trunc(amount) AND
        fee_amount = trunc(fee_amount) AND
        seller_amount = trunc(seller_amount));

ALTER TABLE wallets
    ADD CONSTRAINT wallets_money_whole_rupiah CHECK (
        balance = trunc(balance) AND held_balance = trunc(held_balance));

ALTER TABLE wallet_transactions
    ADD CONSTRAINT wallet_transactions_money_whole_rupiah CHECK (
        amount = trunc(amount) AND balance_after = trunc(balance_after));

ALTER TABLE wallet_topups
    ADD CONSTRAINT wallet_topups_money_whole_rupiah CHECK (amount = trunc(amount));

ALTER TABLE payouts
    ADD CONSTRAINT payouts_money_whole_rupiah CHECK (amount = trunc(amount));

ALTER TABLE refunds
    ADD CONSTRAINT refunds_money_whole_rupiah CHECK (amount = trunc(amount));

ALTER TABLE seller_reservations
    ADD CONSTRAINT seller_reservations_money_whole_rupiah CHECK (amount = trunc(amount));

ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_money_whole_rupiah CHECK (amount = trunc(amount));

ALTER TABLE account_balances
    ADD CONSTRAINT account_balances_money_whole_rupiah CHECK (balance = trunc(balance));

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_money_whole_rupiah;
ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_money_whole_rupiah;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_money_whole_rupiah;
ALTER TABLE wallets DROP CONSTRAINT IF EXISTS wallets_money_whole_rupiah;
ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_money_whole_rupiah;
ALTER TABLE wallet_topups DROP CONSTRAINT IF EXISTS wallet_topups_money_whole_rupiah;
ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_money_whole_rupiah;
ALTER TABLE refunds DROP CONSTRAINT IF EXISTS refunds_money_whole_rupiah;
ALTER TABLE seller_reservations DROP CONSTRAINT IF EXISTS seller_reservations_money_whole_rupiah;
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_money_whole_rupiah;
ALTER TABLE account_balances DROP CONSTRAINT IF EXISTS account_balances_money_whole_rupiah;
