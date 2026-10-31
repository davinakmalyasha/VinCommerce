-- +goose Up
-- A return refund that goes through the provider needs a state for "the
-- provider has been asked, the answer is not back yet". Without it the only
-- choices were to hold the return row locked across the HTTP call (which stalls
-- every other refund) or to mark it refunded before the provider answered
-- (which is a lie when the call fails).
--
-- `refunding` is visible to the buyer and the admin, and a crash between the
-- claim and the provider call leaves the claim recoverable by an operator
-- instead of silently reverting or double-refunding.
ALTER TABLE return_requests DROP CONSTRAINT IF EXISTS return_requests_status_check;
ALTER TABLE return_requests
    ADD CONSTRAINT return_requests_status_check
    CHECK (status IN ('requested', 'approved', 'refunding', 'rejected', 'returned', 'refunded', 'closed'));

-- +goose Down
ALTER TABLE return_requests DROP CONSTRAINT IF EXISTS return_requests_status_check;
ALTER TABLE return_requests
    ADD CONSTRAINT return_requests_status_check
    CHECK (status IN ('requested', 'approved', 'rejected', 'returned', 'refunded', 'closed'));
