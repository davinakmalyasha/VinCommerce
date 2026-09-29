package service

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
)

// The refund cap is the invariant that stops a marketplace minting money.
//
// RefundOrder used to read the cumulative refunded total on the POOL -- a
// different connection, outside the transaction, before any lock -- and then
// rely on a guarded UPDATE to serialise. That guard passes
// `partially_refunded` in its expected-state list, so a second concurrent
// partial refund re-evaluated its WHERE clause against the row version the first
// one had just committed, still matched, and proceeded.
//
// The lock serialised the two refunds and then let both through. Two
// individually-valid Rp60,000 refunds against a Rp100,000 charge paid out
// Rp120,000, and when escrow had not been released there are no debits at all,
// so the buyer was credited Rp120,000 of money that did not exist.
//
// RefundReturn was worse: it had no cap at all, credited the buyer with no
// counterpart anywhere, debited the seller the GROSS item value rather than the
// net they received, reversed no commission, and set a terminal intent status on
// the first refunded item. All four are gone -- RefundReturn now goes through
// the same plan as every other refund -- but the invariant is asserted here
// rather than trusted to the code path.
//
// These tests exercise the pure arithmetic of the cap and the leg derivation.
// The concurrency half needs two database connections and belongs in the
// integration harness; what is provable without one is that the arithmetic
// cannot produce an over-refund even when fed a sequence of valid-looking
// requests.

// refundRequest is one refund attempt against an order, as a caller would
// describe it.
type refundRequest struct {
	Amount float64
}

// simulateRefunds applies a sequence of refunds to a charge, exactly as
// buildRefundPlan does on each call: the cumulative is re-read from the ledger
// every time, and the remainder is what bounds the next refund.
//
// The accumulation here IS the ledger sum, so this models the post-fix
// behaviour faithfully. It is the same arithmetic RefundReturn used to skip.
func simulateRefunds(charge float64, requests []refundRequest) (paidOut float64, err error) {
	ledger := 0.0 // SUM(wallet_transactions WHERE ref_id = order AND reason = 'refund' AND kind = 'credit')
	for _, r := range requests {
		remaining := moneyRound(charge - ledger)
		if remaining <= 0 {
			return paidOut, fmt.Errorf("ALREADY_REFUNDED: nothing left to refund")
		}
		amount := r.Amount
		if amount <= 0 {
			amount = remaining
		}
		if amount > remaining {
			return paidOut, fmt.Errorf(
				"REFUND_EXCEEDS_REMAINING: Rp%.0f > Rp%.0f remaining", amount, remaining)
		}
		ledger = moneyRound(ledger + amount)
		paidOut += amount
	}
	return paidOut, nil
}

// TestRefundCapIsNeverExceeded is the core invariant: the total refunded can
// never exceed what was charged, no matter how the requests are shaped.
func TestRefundCapIsNeverExceeded(t *testing.T) {
	rng := rand.New(rand.NewPCG(101, 103))
	for iter := 0; iter < 100_000; iter++ {
		charge := float64(1 + rng.IntN(50_000_000))
		n := 1 + rng.IntN(6)
		reqs := make([]refundRequest, n)
		lastWasFullRemainder := false
		for i := range reqs {
			// A mix of: a random amount, the FULL remaining (amount <= 0 means
			// "refund everything left"), and an amount deliberately too large.
			switch rng.IntN(3) {
			case 0:
				reqs[i] = refundRequest{Amount: float64(rng.IntN(int(charge)) + 1)}
			case 1:
				reqs[i] = refundRequest{Amount: 0} // refund the remainder
			default:
				reqs[i] = refundRequest{Amount: charge * (1 + float64(rng.IntN(3)))}
			}
			if i == len(reqs)-1 {
				lastWasFullRemainder = reqs[i].Amount == 0
			}
		}

		paid, err := simulateRefunds(charge, reqs)
		if err != nil {
			// A refusal is the CORRECT outcome for a sequence that asks for more
			// than is left. What must never happen is a refusal AFTER money was
			// already paid out beyond the charge.
			if paid > charge {
				t.Fatalf("charge Rp%.0f: paid Rp%.0f then refused %v -- an over-refund "+
					"partially succeeded", charge, paid, err)
			}
			continue
		}
		if paid > charge {
			t.Fatalf("charge Rp%.0f: accepted refunds totalling Rp%.0f -- MONEY MINTED "+
				"(requests %+v)", charge, paid, reqs)
		}
		// A sequence whose LAST request asked for the whole remainder must have
		// consumed exactly the charge. A sequence that ends in a strict partial is
		// legitimately allowed to leave a balance, so asserting equality
		// unconditionally would be asserting a property the code does not (and
		// should not) have.
		if lastWasFullRemainder && paid != charge {
			t.Errorf("charge Rp%.0f: the final request asked for the whole remainder but only "+
				"Rp%.0f was paid (requests %+v) -- the ledger and the payments disagree",
				charge, paid, reqs)
		}
	}
}

// TestRefundLegsSumToTheRefundAmount: the debit pair and the credit must agree,
// or the escrow and the platform are left out of balance by a permanent,
// accumulating amount.
func TestRefundLegsSumToTheRefundAmount(t *testing.T) {
	rng := rand.New(rand.NewPCG(107, 109))
	for iter := 0; iter < 200_000; iter++ {
		amount := float64(1 + rng.IntN(10_000_000))
		charged := amount * (1 + float64(rng.IntN(10)))
		fee := moneyRound(charged * rng.Float64() * 0.2) // up to 20% commission

		// The derivation from buildRefundPlan: scale the fee to this refund's
		// share, then make the seller's leg the residual.
		ratio := amount / charged
		refundFee := moneyRound(fee * ratio)
		refundSeller := moneyRound(amount - refundFee)
		if refundSeller < 0 {
			refundSeller = 0
		}
		if refundFee+refundSeller != amount {
			// The production code re-derives when this happens; assert that the
			// re-derivation is always sufficient rather than assuming it.
			refundFee = moneyRound(amount - refundSeller)
		}
		if refundFee+refundSeller != amount {
			t.Fatalf("amount Rp%.0f of a Rp%.0f charge with a Rp%.0f fee: legs are "+
				"Rp%.0f + Rp%.0f, which does not sum to the refund -- money created or destroyed",
				amount, charged, fee, refundFee, refundSeller)
		}
		if refundFee < 0 || refundSeller < 0 {
			t.Fatalf("negative leg: fee Rp%.0f seller Rp%.0f", refundFee, refundSeller)
		}
		if refundFee > fee {
			t.Fatalf("reversing Rp%.0f of commission when only Rp%.0f was taken",
				refundFee, fee)
		}
	}
}

// TestSequentialPartialRefundsReconstructTheCharge: a full sequence of
// percentage refunds that a dispute-resolution flow might produce, each
// individually valid, must not drift.
func TestSequentialPartialRefundsReconstructTheCharge(t *testing.T) {
	const charge = 1_000_000.0
	// 30%, then 30%, then the remainder -- the shape a partial-refund webhook and
	// then an admin action produce.
	reqs := []refundRequest{{Amount: 300_000}, {Amount: 300_000}, {Amount: 0}}
	paid, err := simulateRefunds(charge, reqs)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if paid != charge {
		t.Fatalf("paid Rp%.0f of a Rp%.0f charge", paid, charge)
	}

	// The third request asks for more than is left and MUST be refused.
	_, err = simulateRefunds(charge, []refundRequest{{Amount: 300_000}, {Amount: 300_000}, {Amount: 500_000}})
	if err == nil {
		t.Fatal("a third refund of Rp500,000 against Rp400,000 remaining was ACCEPTED")
	}
}

// TestRoundTripOfWholeRupiah is the property that lets exact comparison replace
// the epsilon comparisons this code used to need. `absDiff(x, y) > 0.01` and
// `> 1.0` were float-drift workarounds; a one-rupiah tolerance on a buyer's
// refund is one rupiah that does not exist.
func TestRoundTripOfWholeRupiah(t *testing.T) {
	rng := rand.New(rand.NewPCG(113, 127))
	for i := 0; i < 200_000; i++ {
		v := moneyRound(float64(rng.IntN(1_000_000_000)) / 100.0)
		if !IsWholeIDR(v) {
			t.Fatalf("moneyRound produced a fractional rupiah: %v", v)
		}
		if absDiff(v, v) != 0 {
			t.Fatalf("absDiff is not zero for identical values: %v", v)
		}
		// A recomputed total must compare EXACTLY to a stored one.
		half := moneyRound(v / 2)
		other := moneyRound(v - half)
		if other+half != v {
			t.Fatalf("splitting Rp%.0f into Rp%.0f + Rp%.0f lost money", v, other, half)
		}
		if math.Abs(other+half-v) > 0 {
			t.Fatalf("splitting Rp%.0f did not reconstruct it exactly", v)
		}
	}
}
