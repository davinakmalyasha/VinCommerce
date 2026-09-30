package service

import (
	"fmt"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/payments"
)

// Refund CAP decisions, as pure functions.
//
// Every rule about how much may be refunded lives here, separated from the code
// that reads and writes the database. That split is not stylistic: the cumulative
// cap is the single invariant standing between a marketplace and paying the same
// buyer twice, and the version that lived inline could not be tested without a
// database -- which is exactly how it came to measure nothing (see
// `refund_state_counts_toward_cap`).

// providerRefundEvent is a refund the PROVIDER has already performed.
//
// It is distinct from a refund request. A request is something we decided and
// must ask the provider to do; this is a report that the provider already did it,
// arrived as a webhook. Conflating the two is how one notification becomes two
// refunds.
type providerRefundEvent struct {
	OrderID string
	Amount  float64
	// Gateway is the adapter that reported it, so the idempotency key is scoped
	// per provider. Two gateways may legitimately issue the same refund id.
	Gateway string
	// Reference is the provider's own id for THIS refund (Midtrans `refund_id`).
	// It is the only thing that makes the notification identifiable, and
	// therefore the only thing that makes it idempotent.
	Reference string
	Reason    string
}

// providerRefundKey derives the idempotency key for a provider refund.
//
// Keyed on the provider's reference AND the amount, because the reference is
// what the provider guarantees unique and the amount is what distinguishes a
// notification about a real refund from a malformed one.
//
// Returns "" when there is no reference. An unidentifiable money movement is not
// deduplicated by guesswork: two notifications with no reference might be two
// genuine partial refunds, and collapsing them would lose a refund the buyer is
// owed. The caller routes that to a human instead.
func providerRefundKey(ev providerRefundEvent) string {
	ref := strings.TrimSpace(ev.Reference)
	if ref == "" {
		return ""
	}
	return fmt.Sprintf("provider:%s:%s:%.0f", ev.Gateway, ref, ev.Amount)
}

// refundDecision is what to do with a provider refund notification.
type refundDecision string

const (
	// refundDecisionApply: identifiable, and not already recorded.
	refundDecisionApply refundDecision = "apply"
	// refundDecisionAlreadyApplied: we have already recorded this exact refund.
	// A replayed webhook is normal -- Midtrans retries for up to 24 hours -- and
	// must be a no-op, or the one event that happens most often defeats the
	// idempotency the whole refunds table exists to provide.
	refundDecisionAlreadyApplied refundDecision = "already_applied"
	// refundDecisionManual: the provider moved money we cannot attribute to a
	// refund we recorded. A person has to reconcile it.
	refundDecisionManual refundDecision = "manual"
)

// classifyProviderRefund decides what to do with a notification given whether we
// have already recorded a refund under its key.
//
// `alreadyRecorded` is the answer to "does a `refunds` row exist with this
// idempotency key?", which is a question about the database and therefore asked by
// the caller. Everything downstream of it is a decision, and decisions are what
// this file holds.
func classifyProviderRefund(ev providerRefundEvent, alreadyRecorded bool) refundDecision {
	if strings.TrimSpace(ev.Reference) == "" {
		// Unidentifiable. Applying it risks a duplicate we cannot detect, and
		// refusing it risks losing a refund the buyer is owed. Neither is
		// acceptable unattended, so it becomes visible.
		return refundDecisionManual
	}
	if alreadyRecorded {
		return refundDecisionAlreadyApplied
	}
	return refundDecisionApply
}

// classifyUnidentifiableProviderRefund is the no-reference case on its own.
//
// Split out because it is the one decision a test can make without a database at
// all, and it is the one that must never be `apply`.
func classifyUnidentifiableProviderRefund(ev providerRefundEvent) refundDecision {
	return classifyProviderRefund(ev, false)
}

// refundStateCountsTowardCap reports whether a refund in this state represents
// money that has left, or is committed to leaving.
//
// THE BUG THIS FIXES. The cumulative cap used to read
// `wallet_transactions WHERE kind = 'credit'`. A refund through the GATEWAY never
// credits the buyer's wallet -- the money goes back to their card -- so after two
// Rp30,000 gateway refunds the cap believed nothing had been refunded at all, and
// happily authorised a third. The guard built specifically to stop
// over-refunding a buyer measured nothing on the path that actually refunds them.
//
// States, and the reasoning:
//
//	pending    NOT counted. Written before the provider is called; if the submit
//	           fails nothing moved, and counting it would refuse a legitimate
//	           retry.
//	submitted  counted. The provider accepted it and will pay. Not counting it
//	           would let a second refund be authorised while the first is in
//	           flight, which is precisely the double-refund the cap exists for.
//	succeeded  counted. The money is gone.
//	failed     NOT counted. The provider rejected it; no money moved.
//	manual     counted. We OWE the buyer this money and a human has to send it.
//	           Not counting it would let the platform promise the same rupiah to
//	           two buyers, and it would do so while showing a clean cap.
func refundStateCountsTowardCap(status string) bool {
	switch status {
	case RefundStateSubmitted, RefundStateSucceeded, RefundStateManual:
		return true
	default:
		return false
	}
}

// capDecision is the outcome of checking a requested refund against what has
// already been refunded.
type capDecision struct {
	// Amount is what will actually be refunded: the request, or the whole
	// remaining balance when the request was zero.
	Amount float64
	// Remaining is what is left of the charge afterwards.
	Remaining float64
	// Partial reports whether anything is left, which is what makes this a partial
	// rather than a full refund.
	Partial bool
}

// decideRefundCap is the single implementation of the cumulative cap.
//
// It used to live inline in buildRefundPlan, wrapped around a database read, so
// the rule that decides how much money may leave the platform could only be
// tested with a database. Both properties below were found that way.
//
// The cap is EXACT. With whole-rupiah money an epsilon is not a rounding
// tolerance, it is a rupiah of money that does not exist: a `> 0.01` comparison
// on a buyer's refund is a sen the platform pays out and cannot account for.
func decideRefundCap(charge, alreadyRefunded, requested float64) (capDecision, error) {
	remaining := moneyRound(charge - alreadyRefunded)
	if remaining <= 0 {
		return capDecision{}, domain.E(domain.KindConflict, "ALREADY_REFUNDED",
			"this order has already been fully refunded")
	}

	amount := requested
	if amount <= 0 {
		// Zero means "refund whatever is left", which is what a full refund and
		// the gateway's own `payment.refunded` notification both mean.
		amount = remaining
	}
	amount = moneyRound(amount)
	if amount > remaining {
		return capDecision{}, domain.E(domain.KindConflict, "REFUND_EXCEEDS_REMAINING",
			fmt.Sprintf("refund Rp%.0f exceeds the remaining refundable Rp%.0f of Rp%.0f charged",
				amount, remaining, charge))
	}

	after := moneyRound(remaining - amount)
	return capDecision{Amount: amount, Remaining: after, Partial: after > 0}, nil
}

// deriveReversalLegs splits a refund into the two legs that reverse a prior
// escrow release: the platform's commission and the seller's net.
//
// A pure function, extracted from what was inline arithmetic in a function that
// required a database. A mutation that dropped the commission leg -- leaving the
// platform its fee on a fully refunded sale, the exact defect `03a100b` fixed --
// passed the entire suite while this was inline, because the only way to reach the
// arithmetic was through a live transaction.
//
// The legs sum to `amount` EXACTLY, and the way they are made to is deliberate:
//
//	refundFee     = round(fee x amount/charge)
//	refundSeller  = amount - refundFee          <- the RESIDUAL
//
// Rounding both independently drifts by a sen per partial refund, and the drift
// accumulates permanently in the wallet balances: there is no later sweep that
// would ever collect it.
func deriveReversalLegs(fee, charge, amount float64) (refundFee, refundSeller float64) {
	if amount <= 0 || charge <= 0 {
		return 0, 0
	}
	refundFee = moneyRound(fee * (amount / charge))
	refundSeller = moneyRound(amount - refundFee)
	if refundSeller < 0 {
		// The scaled fee exceeded the refund itself. Clamp rather than go negative:
		// a negative seller leg is a credit TO the seller on a refund.
		refundSeller = 0
	}
	// Make the pair sum exactly even if the residual fell outside the leg. Only
	// reachable through float edges, but a pair that does not sum means money was
	// created or destroyed.
	if refundFee+refundSeller != amount {
		refundFee = moneyRound(amount - refundSeller)
	}
	return refundFee, refundSeller
}

// providerRefundInputFor builds the provider request for a refund we INITIATE.
//
// The name is the warning: there is a separate path for refunds the provider has
// already performed, and reaching for this one from a webhook is the double-refund
// that `ac351cf` introduced.
func providerRefundInputFor(intent *domain.PaymentIntent, amount float64, reason string) payments.RefundInput {
	return payments.RefundInput{
		Reference: chargeReference(intent),
		Amount:    amount,
		Currency:  intent.Currency,
		Reason:    reason,
		// Keyed on the amount, so a second partial refund of a DIFFERENT amount
		// is a different key, and a retry of the same one is recognised.
		IdempotencyKey: fmt.Sprintf("refund:%s:%.0f", intent.ID, moneyRound(amount)),
	}
}
