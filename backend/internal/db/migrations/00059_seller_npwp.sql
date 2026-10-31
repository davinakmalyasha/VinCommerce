-- +goose Up
-- Seller NPWP.
--
-- The invoice could print PPN but not the seller's tax identity, which is what
-- makes it a faktur rather than a receipt. Optional: an individual seller
-- without a registered NPWP is a legitimate case, so the column is nullable and
-- the invoice prints the line only when one exists. Silently printing
-- "NPWP: -" on a tax document is worse than omitting it.
ALTER TABLE seller_kyc ADD COLUMN IF NOT EXISTS npwp VARCHAR(20);

-- +goose Down
ALTER TABLE seller_kyc DROP COLUMN IF EXISTS npwp;
