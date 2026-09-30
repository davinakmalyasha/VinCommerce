package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
)

// Refund execution: getting the money back to the buyer THROUGH the provider.
//
// What this replaces: every refund was an internal book transfer that credited a
// wallet balance. A buyer who paid by QRIS or bank transfer was refunded in
// platform credit they could not spend or withdraw, and the money stayed with
// the platform. For Midtrans that is also a terms-of-service breach -- refunds
// go back through the provider.
//
// The hard part is not calling the API. It is that a provider call sits OUTSIDE
// a database transaction, so the system has to survive the gap: the process can
// die between "we decided to refund" and "the provider confirmed". That is what
// the `refunds` table is for, and why the flow is:
//
//	1. reserve   (transaction) write a `refunds` row, status pending
//	2. submit    (no transaction) call the gateway
//	3. settle    (transaction) record the outcome, move the money, post the ledger
//
// Step 1 commits BEFORE the gateway is touched, so a crash after step 2 leaves a
// pending row that reconciliation can resolve from the provider's own view. The
// alternative -- calling the gateway first and recording afterwards -- loses the
// record exactly when it matters most: a refund the provider performed that we
// never wrote down, which is a real outflow of money with no accounting entry.

// Refund statuses as stored. Aliased from domain rather than redeclared.
//
// These were originally a second, independent set in this package, and having two
// sets of the same five strings is how a state constant drifts from the column's
// CHECK constraint: the database rejects the value, in production, on a real
// refund, and the error says nothing about which of the two definitions is
// wrong. domain owns the vocabulary; this package and the handlers both use it.
const (
	RefundStatePending   = domain.RefundPending
	RefundStateSubmitted = domain.RefundSubmitted
	RefundStateSucceeded = domain.RefundSucceeded
	RefundStateFailed    = domain.RefundFailed
	RefundStateManual    = domain.RefundManual
)

// refundLookup finds a refund we already recorded for a provider notification.
//
// An interface so the REPLAY property is testable. The rule "a notification we
// have already recorded is a no-op" is the thing standing between a provider's
// 24-hour retry loop and paying a buyer twice, and the only way to assert it
// without a database is to be able to supply the recorded row directly.
//
// This exists because the wiring `classifyProviderRefund(ev, already != nil)`
// survived mutation testing: a unit test of the classifier passes `true` as a
// literal and never exercises the lookup that produces it. A test of the CLASSIFIER
// is not a test of the GUARD.
type refundLookup interface {
	RefundByProviderKey(ctx context.Context, intentID, key string) (*repository.Refund, error)
}

// paymentRefundLookup adapts the repository to refundLookup.
type paymentRefundLookup struct {
	payments *repository.PaymentRepository
}

func (l paymentRefundLookup) RefundByProviderKey(
	ctx context.Context, intentID, key string,
) (*repository.Refund, error) {
	return l.payments.RefundByProviderKey(ctx, l.payments.Pool(), intentID, key)
}

// replayIsRecognised resolves a notification's idempotency key, consults the
// lookup, and reports the refund we already recorded for it.
//
// `intentID` is an ARGUMENT rather than something this function re-derives,
// because deriving it here is what broke the guard: it used to pass a literal ""
// and the lookup filters `WHERE payment_intent_id = $1`. `payment_intent_id` is
// `UUID NOT NULL`, so an empty string is not "a payment with no refunds" -- it is
// an invalid uuid, and the query failed rather than matching. Every provider
// refund notification errored out before recording anything, and Midtrans retried
// for 24 hours. The caller already has the intent in hand, so it passes the id it
// actually read.
//
// Extracted as a named seam because this is the guard standing between a provider's
// retry loop and paying a buyer twice, and it was the one piece of the fix that
// mutation testing could not reach: the handoff between the lookup and the
// classifier lives in a function that also needed a database to enter.
//
// Returning the recorded refund (rather than a bool) is what makes the replay
// path useful -- the caller can return it to the provider, which stops the retry
// instead of provoking another notification.
func (s *PaymentService) replayIsRecognised(
	ctx context.Context, intentID string, ev providerRefundEvent,
) (*repository.Refund, error) {
	key := providerRefundKey(ev)
	already, err := s.refundRecordedForProviderKey(ctx, intentID, key)
	if err != nil {
		return nil, err
	}
	if classifyProviderRefund(ev, already != nil) == refundDecisionAlreadyApplied {
		return already, nil
	}
	return nil, nil
}

// RecordProviderRefund records a refund the PROVIDER has already performed.
//
// It never calls the gateway. That is the entire point of this function existing
// separately from ExecuteRefund.
//
// `ac351cf` routed provider refund NOTIFICATIONS through ExecuteRefund, which is
// the path for refunds we initiate. So every `payment.refunded` webhook asked
// Midtrans to refund the same money a second time. A full refund is merely
// noisy -- the provider caps cumulative refunds at the charge and rejects it. A
// partial one is not:
//
//	charge Rp100,000
//	provider refunds Rp30,000 and notifies us
//	we call Refund(Rp30,000) again
//	provider: 30,000 + 30,000 = 60,000 <= 100,000  ->  ACCEPTED
//	buyer has received Rp60,000 for a Rp100,000 order
//
// The idempotency key is the provider's own refund reference, so a replayed
// webhook -- which Midtrans sends for up to 24 hours -- is a no-op rather than a
// second refund. A notification with no reference cannot be attributed to a
// refund we recorded, and is escalated rather than guessed at.
func (s *PaymentService) RecordProviderRefund(ctx context.Context, ev providerRefundEvent) (*repository.Refund, error) {
	if ev.Amount <= 0 {
		return nil, domain.E(domain.KindInvalid, "REFUND_AMOUNT_REQUIRED",
			"a provider refund notification must carry the amount that was refunded")
	}
	intent, err := s.payments.IntentByOrder(ctx, ev.OrderID)
	if err != nil {
		return nil, err
	}
	if ev.Gateway == "" {
		ev.Gateway = intent.Gateway
	}

	// The replay guard, via the same seam a test drives. A provider retries for up
	// to 24 hours, so a duplicate notification is the most common event in the
	// system; without this it is also the most expensive.
	//
	// `intent.ID`, not a literal: the guard is scoped to this payment, and a
	// lookup that cannot be scoped cannot answer the question.
	replayed, err := s.replayIsRecognised(ctx, intent.ID, ev)
	if err != nil {
		return nil, err
	}
	if replayed != nil {
		return replayed, nil
	}

	switch classifyProviderRefund(ev, false) {
	case refundDecisionAlreadyApplied:
		// Unreachable: replayIsRecognised returned above if a refund was recorded.
		// Kept explicit rather than collapsed, because deleting the branch would
		// make a future lookup regression fall through to APPLY -- turning a replay
		// into a second refund instead of a no-op.
		return nil, domain.E(domain.KindInternal, "REPLAY_GUARD_BYPASSED",
			"a provider refund was already recorded but the replay guard did not "+
				"recognise it; refusing rather than risking a duplicate refund")

	case refundDecisionManual:
		// The provider moved money we cannot attribute. Record it so the amount is
		// known, and make it a person's problem: the alternative is applying an
		// unidentifiable movement, which is a duplicate waiting to happen.
		refund := &repository.Refund{
			ID:              newRefundID(),
			PaymentIntentID: intent.ID,
			OrderID:         intent.OrderID,
			Gateway:         ev.Gateway,
			Amount:          moneyRound(ev.Amount),
			Reason:          ev.Reason,
			Status:          RefundStateManual,
			RequestedAt:     time.Now().UTC(),
		}
		if err := s.payments.CreateRefund(ctx, s.payments.Pool(), refund); err != nil {
			return nil, err
		}
		s.writeRefundStatus(ctx, refund.ID, "", RefundStateManual,
			"the provider reported a refund with no reference, so it cannot be "+
				"matched against a refund we recorded; an operator must confirm it")
		return refund, domain.E(domain.KindConflict, "REFUND_UNIDENTIFIABLE",
			"the gateway reported a refund with no reference; an operator must reconcile it")
	}

	// Identifiable and not yet recorded: the provider has moved the money, so the
	// only thing left is to bring OUR books into line.
	refund := &repository.Refund{
		ID:              newRefundID(),
		PaymentIntentID: intent.ID,
		OrderID:         intent.OrderID,
		Gateway:         ev.Gateway,
		GatewayRef:      strings.TrimSpace(ev.Reference),
		Amount:          moneyRound(ev.Amount),
		Reason:          ev.Reason,
		// `submitted` rather than `succeeded`: the provider has told us the money
		// moved, but this system has not yet derived anything from that, and the
		// cap counts `submitted` -- so the money is committed the moment the
		// notification lands, which is when the risk actually exists.
		Status:      RefundStateSubmitted,
		RequestedAt: time.Now().UTC(),
	}
	if err := s.payments.CreateRefund(ctx, s.payments.Pool(), refund); err != nil {
		return nil, err
	}

	plan := &refundPlan{Amount: moneyRound(ev.Amount)}
	if err := s.settleProviderRefund(ctx, refund, intent, plan, ev.Reason); err != nil {
		return refund, err
	}
	return refund, nil
}

// refundRecordedForProviderKey finds a refund we already recorded for a provider
// notification.
//
// The key is the provider's own refund reference, which is what the provider
// guarantees unique. Matching on the amount would collapse two genuine partial
// refunds of the same size into one, silently losing one.
//
// `intentID` scopes the search, and it is validated rather than assumed. An empty
// one is not a payment with no refunds; `payment_intent_id` is `UUID NOT NULL`, so
// the lookup's `WHERE payment_intent_id = $1` receives an invalid uuid and the
// query ERRORS. An error here aborts the whole refund, so the guard is loud rather
// than permissive: a caller that cannot say which payment it is asking about has
// no business asking. This function previously passed a literal "" and every
// provider refund notification failed that way.
//
// An empty KEY is different, and is a no-op rather than an error: there is no
// provider reference to match on, the notification is unidentifiable, and
// `classifyProviderRefund` escalates it to a human. Nothing to look up is the
// correct answer there, not a wiring fault.
func (s *PaymentService) refundRecordedForProviderKey(
	ctx context.Context, intentID, key string,
) (*repository.Refund, error) {
	if key == "" {
		return nil, nil
	}
	if strings.TrimSpace(intentID) == "" {
		return nil, domain.E(domain.KindInternal, "REFUND_LOOKUP_WITHOUT_INTENT",
			"a provider refund replay lookup was made without a payment intent id, "+
				"so it cannot be scoped to a payment; refusing rather than searching "+
				"for a refund across every order")
	}
	lookup := s.refunds
	if lookup == nil {
		// A nil lookup is a wiring mistake, not a reason to proceed: without it
		// every notification would look new and a replay would be applied again,
		// which is the exact bug this function exists to close. Falling back to the
		// repository keeps production correct while making the omission loud.
		lookup = paymentRefundLookup{payments: s.payments}
	}
	return lookup.RefundByProviderKey(ctx, intentID, key)
}

// ExecuteRefund runs a refund through the provider and records it durably.
//
// This is the path for refunds WE initiate. A notification that the provider has
// already performed must go to RecordProviderRefund, which never calls the
// gateway -- see the comment there for what happens when it does.
//
// refundToBuyer=false records the decision WITHOUT calling the gateway: the
// platform absorbs the cost. That path writes a `refunds` row so the amount is
// still accounted for and an operator can see the write-off, rather than the
// money silently vanishing into an expense.
func (s *PaymentService) ExecuteRefund(
	ctx context.Context, orderID, reason string, refundToBuyer bool, amount float64, requestedBy string,
) (*repository.Refund, error) {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if intent.GatewayRef == "" {
		return nil, domain.E(domain.KindConflict, "NO_GATEWAY_REFERENCE",
			"this payment has no gateway reference, so it cannot be refunded through the provider; "+
				"an operator must reconcile it manually")
	}

	// --- step 1: reserve -------------------------------------------------------
	//
	// The amount and the cumulative cap are derived under the intent row lock, and
	// the pending row is written in the SAME transaction. Two concurrent refunds
	// therefore cannot both read the same remaining balance: the second blocks on
	// the lock and then sees the first one's row.
	refund, plan, err := s.reserveRefund(ctx, intent, reason, refundToBuyer, amount, requestedBy)
	if err != nil {
		return nil, err
	}

	if !refundToBuyer {
		// A write-off: the money is deliberately not returned. Settle it here so
		// the record is complete, and let the caller apply the internal legs.
		return refund, nil
	}

	// --- step 2: submit (outside any transaction) ------------------------------
	gw, err := s.gatewayFor(intent.Gateway)
	if err != nil {
		// A missing or unusable adapter is a MANUAL case, not a failure: the
		// platform owes the buyer and a human has to move the money. Recording
		// it as failed would suggest a retry would fix it, which it would not.
		s.markRefundManual(ctx, refund.ID, "gateway adapter unavailable: "+err.Error())
		return refund, domain.E(domain.KindConflict, "GATEWAY_UNAVAILABLE",
			"the payment gateway is unavailable; the refund is recorded for manual completion")
	}

	res, err := gw.Refund(ctx, gatewayRefundInput(intent, plan, reason))
	if err != nil {
		// A transport error is ambiguous: the provider may have accepted it. It is
		// recorded as submitted, not failed, so reconciliation resolves it from the
		// provider's view. Marking it failed would invite a retry that double-refunds.
		s.markRefundSubmitted(ctx, refund.ID, "transport error: "+err.Error())
		return refund, domain.E(domain.KindConflict, "GATEWAY_ERROR",
			"the refund could not be confirmed with the gateway; it is recorded as submitted "+
				"and will be reconciled rather than retried blindly")
	}

	// --- step 3: settle --------------------------------------------------------
	outcome := classifyGatewayResult(res)
	if !outcome.settle {
		s.updateRefundOutcome(ctx, refund, res, outcome.status)
		if outcome.status == RefundStateSubmitted {
			// Intentional: the refund is recorded and the provider is working on it.
			// Returning an error would make an admin click look like a failure and
			// invite a retry -- the one thing that must not happen with a refund.
			// Reconciliation resolves it from the provider's own view.
			return refund, nil
		}
		if outcome.status == RefundStateManual {
			return refund, domain.E(domain.KindConflict, "REFUND_NEEDS_OPERATOR",
				"the gateway could not complete this refund automatically; an operator must "+
					"transfer the money to the buyer")
		}
		return refund, domain.E(domain.KindConflict, "REFUND_REJECTED",
			"the gateway rejected the refund: "+res.Message)
	}
	return refund, s.completeRefund(ctx, refund, intent, plan, res, reason, true)
}

// updateRefundOutcome records a non-settling outcome against the refunds row.
func (s *PaymentService) updateRefundOutcome(
	ctx context.Context, refund *repository.Refund, res *payments.RefundResult, status string,
) {
	reason := ""
	if res != nil {
		reason = res.Message
	}
	switch status {
	case RefundStateSubmitted:
		ref := ""
		if res != nil {
			ref = res.ProviderRef
		}
		s.markRefundSubmitted(ctx, refund.ID, ref)
	case RefundStateManual:
		s.markRefundManual(ctx, refund.ID, reason)
	default:
		s.markRefundFailed(ctx, refund.ID, reason)
	}
}

// reserveRefund writes the pending `refunds` row and derives the amount under the
// intent lock. Returns the row and the plan so the caller can reuse the exact
// figures rather than re-deriving them.
func (s *PaymentService) reserveRefund(
	ctx context.Context, intent *domain.PaymentIntent, reason string,
	refundToBuyer bool, amount float64, requestedBy string,
) (*repository.Refund, *refundPlan, error) {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	locked, err := s.payments.LockIntentForRefund(ctx, q, intent.ID)
	if err != nil {
		return nil, nil, err
	}
	// nil: the row for THIS refund does not exist yet. Deriving the cap before
	// writing it is the point -- see settleProviderRefund and completeRefund for
	// the same plan derived AFTER the row is in the table, and why they must
	// exclude it.
	plan, err := s.buildRefundPlan(ctx, q, locked, amount, nil)
	if err != nil {
		return nil, nil, err
	}

	refund := &repository.Refund{
		ID:              newRefundID(),
		PaymentIntentID: locked.ID,
		OrderID:         locked.OrderID,
		Gateway:         locked.Gateway,
		// GatewayRef is left empty on purpose: it holds the provider's id for
		// THIS refund, which does not exist until the gateway assigns one. The
		// charge being refunded is on the intent.
		Amount:      plan.Amount,
		Reason:      reason,
		Status:      RefundStatePending,
		RequestedBy: requestedBy,
		RequestedAt: time.Now().UTC(),
	}
	if refundToBuyer {
		refund.Status = RefundStateSubmitted
	}

	if err := s.payments.CreateRefund(ctx, q, refund); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return refund, plan, nil
}

// settleProviderRefund brings our books into line with a refund the provider has
// already performed.
//
// Deliberately a SEPARATE function from completeRefund rather than a shared one
// with a flag. They look almost identical -- both lock the intent, re-derive the
// plan, move the internal legs and post a journal -- and merging them is exactly
// how the double-refund came back: one function, two callers, one of which must
// not submit. A flag would put `submitToGateway bool` in the signature and invite
// the next person to pass the wrong value. Two named functions make the wrong
// call a compile error rather than a duplicate refund.
//
// The internal legs are the same either way: the money left the platform to the
// buyer's card, so what our side owes is the seller's net and our commission, and
// the buyer is credited by the provider rather than by us.
func (s *PaymentService) settleProviderRefund(
	ctx context.Context, refund *repository.Refund, intent *domain.PaymentIntent,
	plan *refundPlan, reason string,
) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	order, err := s.orders.ByID(ctx, refund.OrderID)
	if err != nil {
		return err
	}

	// Lock first, then re-derive: another refund may have been recorded between
	// the notification arriving and this transaction starting, and the cap must be
	// enforced against what is true now.
	locked, err := s.payments.LockIntentForRefund(ctx, q, intent.ID)
	if err != nil {
		return err
	}
	// The provider's figure is authoritative here, but it is still bounded by what
	// is left. A provider that reported more than was charged is a finding, not a
	// reason to pay it: an uncapped trust in the number turns a provider bug into
	// the platform's loss.
	//
	// Excluding this refund's own row is what makes that bound mean anything. The
	// row was inserted as `submitted` by RecordProviderRefund BEFORE this
	// transaction, and `submitted` counts toward the cap -- so including it made a
	// full provider refund read `already = charge`, return ALREADY_REFUNDED, and
	// strand the refund as `manual` with no internal legs reversed and no journal
	// posted, for money the provider had already sent back. The provider path was
	// broken for full refunds.
	already, err := s.refundedTotal(ctx, q, order.ID, &refund.ID)
	if err != nil {
		return err
	}
	cap, err := decideRefundCap(locked.Amount, already, plan.Amount)
	if err != nil {
		// The provider says it refunded more than we think was outstanding. Record
		// the row as manual so the discrepancy is visible and a person resolves
		// it, rather than either paying it or silently discarding the notice.
		s.markRefundManual(ctx, refund.ID,
			fmt.Sprintf("the gateway reported Rp%.0f but only Rp%.0f remains refundable; "+
				"an operator must reconcile the difference", plan.Amount,
				moneyRound(locked.Amount-already)))
		return err
	}
	final := s.refundPlanFor(ctx, q, locked, cap)

	if err := s.payments.SetIntentStatusGuardedTx(ctx, q, locked.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded},
		final.NextStatus); err != nil {
		return err
	}

	if err := s.reverseDisbursement(ctx, q, order, final); err != nil {
		return err
	}
	s.postRefundJournal(ctx, q, order, locked, final, true, reason)

	settled := time.Now().UTC()
	if err := s.payments.SettleRefund(ctx, q, refund.ID, refund.GatewayRef,
		RefundStateSucceeded, "", &settled, nil); err != nil {
		return err
	}

	payStatus := domain.PaymentRefunded
	if final.Partial {
		payStatus = domain.PaymentPartiallyRefunded
	}
	if err := tx.SetPaymentStatus(ctx, order.ID, payStatus); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: order.ID, FromStatus: order.Status, ToStatus: order.Status,
		Note: fmt.Sprintf("refund confirmed by gateway (Rp %.0f): %s", final.Amount, reason),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reverseDisbursement claws back what escrow release credited, if it was
// released.
//
// A refund before the release takes the money out of escrow, so there is nothing
// to claw back. A refund AFTER it has already paid the seller and the platform,
// so both have to give it back -- and only then does the buyer get the money from
// the provider.
//
// Reversing only the seller's leg is how a platform keeps commission on a sale it
// refunded in full.
func (s *PaymentService) reverseDisbursement(
	ctx context.Context, q repository.Querier, order *domain.Order, plan *refundPlan,
) error {
	if !plan.WasReleased {
		return nil
	}
	if plan.RefundSeller > 0 {
		if err := s.payments.WalletTxOn(ctx, q, order.SellerID, "debit",
			domain.TxReasonRefund, plan.RefundSeller, order.ID); err != nil {
			return err
		}
	}
	if plan.RefundFee > 0 {
		if err := s.payments.WalletTxOn(ctx, q, platformWalletID, "debit",
			domain.TxReasonRefund, plan.RefundFee, order.ID); err != nil {
			return err
		}
	}
	return nil
}

// refundPlanFor fills a refundPlan from an already-decided cap, deriving the two
// reversal legs.
//
// The legs are derived so that they sum to the refund amount EXACTLY: the fee is
// scaled to this refund's share of the charge and the seller's leg is the
// RESIDUAL. Rounding two independently-scaled values drifts by a sen per partial
// refund, and the drift accumulates permanently in the wallet balances.
func (s *PaymentService) refundPlanFor(
	ctx context.Context, q repository.Querier, intent *domain.PaymentIntent, cap capDecision,
) *refundPlan {
	plan := &refundPlan{
		Amount:    cap.Amount,
		Remaining: cap.Remaining,
		Partial:   cap.Partial,
	}
	if cap.Remaining > 0 {
		plan.NextStatus = domain.IntentPartiallyRefunded
	} else {
		plan.NextStatus = domain.IntentRefunded
	}

	released, err := s.escrowWasReleasedQuerier(ctx, q, intent.OrderID)
	if err != nil {
		// Treating an unreadable release state as "not released" is the safe
		// direction: no clawback attempted, rather than a clawback of money the
		// seller was correctly paid.
		slogRefundStatus(ctx, intent.ID, RefundStateSucceeded, err)
		return plan
	}
	plan.WasReleased = released
	if !released || cap.Amount <= 0 {
		return plan
	}

	fee, sellerAmount := intent.FeeAmount, intent.SellerAmount
	if !intent.CommissionComputed {
		// The rate snapshot is the source of truth; a pre-00042 intent falls back
		// to the active rate. Recomputing from today's rate would reverse a
		// commission that was never charged.
		if cfg, ferr := s.payments.ActiveFeeTx(ctx, q); ferr == nil {
			fee, sellerAmount = repository.Commission(cfg.Pct, cfg.Fixed, cap.Amount)
		}
	}

	plan.RefundFee, plan.RefundSeller = deriveReversalLegs(fee, intent.Amount, cap.Amount)
	plan.Fee, plan.SellerAmount = fee, sellerAmount
	return plan
}

// completeRefund applies the internal effect of a refund the provider confirmed.
//
// The money movements and the ledger journal are in the transaction that marks
// the refund settled, so a settled `refunds` row and the money that moved for it
// are the same event.
func (s *PaymentService) completeRefund(
	ctx context.Context, refund *repository.Refund, intent *domain.PaymentIntent, plan *refundPlan,
	res *payments.RefundResult, reason string, refundToBuyer bool,
) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	order, err := s.orders.ByID(ctx, refund.OrderID)
	if err != nil {
		return err
	}

	// Re-derive under the lock rather than trusting the earlier plan: between the
	// reserve and the provider confirming, another refund may have landed. The
	// cap must be enforced against what is true NOW, not what was true when the
	// request was made.
	//
	// `&refund.ID` excludes this refund's own row. The reserve step committed it
	// as `submitted` and the provider has now moved the money, so counting it here
	// charged the refund for itself: a full refund of a Rp100,000 charge read
	// `already = 100,000`, found `remaining = 0`, and refused with
	// ALREADY_REFUNDED -- rolling back the reversal legs and the journal while the
	// buyer had already been paid. Every full refund failed, after the money left.
	locked, err := s.payments.LockIntentForRefund(ctx, q, intent.ID)
	if err != nil {
		return err
	}
	final, err := s.buildRefundPlan(ctx, q, locked, plan.Amount, &refund.ID)
	if err != nil {
		return err
	}

	if err := s.payments.SetIntentStatusGuardedTx(ctx, q, locked.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded},
		final.NextStatus); err != nil {
		return err
	}

	// The internal legs, via the same helper the provider-notification path uses.
	// Previously duplicated inline here, which is how a fix to one copy could leave
	// the other wrong -- and the two paths process the same money.
	if err := s.reverseDisbursement(ctx, q, order, final); err != nil {
		return err
	}

	s.postRefundJournal(ctx, q, order, locked, final, refundToBuyer, reason)

	settled := time.Now().UTC()
	if err := s.payments.SettleRefund(ctx, q, refund.ID, res.ProviderRef,
		RefundStateSucceeded, "", &settled, marshalRefundRaw(res.Raw)); err != nil {
		return err
	}

	payStatus := domain.PaymentRefunded
	if final.Partial {
		payStatus = domain.PaymentPartiallyRefunded
	}
	if err := tx.SetPaymentStatus(ctx, order.ID, payStatus); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: order.ID, FromStatus: order.Status, ToStatus: order.Status,
		Note: fmt.Sprintf("refund issued via gateway (Rp %.0f): %s", final.Amount, reason),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// markRefundSubmitted / markRefundFailed / markRefundManual record an outcome
// that did NOT move money.
//
// Each is best-effort by design. The refund row already exists and is the
// durable record; a failure to write its status is worth logging loudly but must
// not be reported as a refund failure, because the caller would then retry a
// refund the provider may already have performed.
// refundStatusWriter records a refund's state transition.
//
// An interface rather than a direct call into the repository, because the
// decision of WHICH transition to make must be testable without a database. Two
// of the mutations that survived earlier -- an absent refund left pending, and an
// ambiguous one settled by guessing -- are decisions in
// classifyProviderRefunds, and they are only reachable in a test if the write
// side can be observed and stubbed.
type refundStatusWriter interface {
	WriteRefundStatus(ctx context.Context, refundID, gatewayRef, status, failureReason string) error
}

// paymentRefundStatuses adapts the repository to refundStatusWriter.
type paymentRefundStatuses struct {
	payments *repository.PaymentRepository
}

func (a paymentRefundStatuses) WriteRefundStatus(
	ctx context.Context, refundID, gatewayRef, status, failureReason string,
) error {
	return a.payments.UpdateRefundStatus(ctx, a.payments.Pool(), refundID, gatewayRef, status, failureReason, nil)
}

func (s *PaymentService) markRefundSubmitted(ctx context.Context, id, gatewayRef string) {
	s.writeRefundStatus(ctx, id, gatewayRef, RefundStateSubmitted, "awaiting gateway settlement")
}

func (s *PaymentService) markRefundFailed(ctx context.Context, id, failureReason string) {
	s.writeRefundStatus(ctx, id, "", RefundStateFailed, failureReason)
}

func (s *PaymentService) markRefundManual(ctx context.Context, id, failureReason string) {
	s.writeRefundStatus(ctx, id, "", RefundStateManual, failureReason)
}

// writeRefundStatus records a transition, tolerating a service with no writer.
//
// A nil writer is tolerated so the decision logic can be exercised without a
// database. It is not tolerated silently in production: the log below is loud,
// and the refund row is already durable from the reserve step.
func (s *PaymentService) writeRefundStatus(
	ctx context.Context, id, gatewayRef, status, failureReason string,
) {
	if s.statuses == nil {
		slogRefundStatus(ctx, id, status,
			fmt.Errorf("no refund status writer is configured"))
		return
	}
	if err := s.statuses.WriteRefundStatus(ctx, id, gatewayRef, status, failureReason); err != nil {
		slogRefundStatus(ctx, id, status, err)
	}
}

// AdminRefunds lists refunds for the operator queue.
//
// status empty means all states, which is what an operator wants by default: the
// queue is a triage list, and pre-filtering to one state hides the ones that fell
// out of every pipeline.
func (s *PaymentService) AdminRefunds(ctx context.Context, status string, limit int) ([]*domain.Refund, error) {
	return s.payments.RefundsForAdmin(ctx, status, limit)
}

// StaleRefundAfter is how long a refund may sit with the provider before the
// system stops calling it "in progress" and starts calling it a person's problem.
//
// Midtrans settles T+1 to T+7 depending on the method, so a week is the outer
// bound; past it, a refund still marked `submitted` is not slow, it is stuck.
// The threshold exists because "in progress" is a claim the system keeps making
// indefinitely on the buyer's behalf, and a claim that never expires is a way of
// never having to admit a refund was lost.
const StaleRefundAfter = 7 * 24 * time.Hour

// AdminRefundSummary is the headline the operator's dashboard needs.
//
// Counts per state, plus the number that actually requires action. A dashboard
// showing "47 refunds, 3 failed" sends the reader somewhere to work; one showing
// "5 need a human" is the list.
type AdminRefundSummary struct {
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	Submitted   int `json:"submitted"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Manual      int `json:"manual"`
	NeedsAction int `json:"needs_action"`
	// StaleSubmitted is the subset of `submitted` that has passed the settlement
	// window. Counted separately from Manual because the remedy differs: a manual
	// refund needs a bank transfer, a stale one needs the gateway chased first.
	StaleSubmitted int `json:"stale_submitted"`
}

// RefundQueueSummary counts the refund queue and identifies what needs a person.
func (s *PaymentService) RefundQueueSummary(ctx context.Context) (*AdminRefundSummary, error) {
	refunds, err := s.payments.RefundsForAdmin(ctx, "", 500)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := &AdminRefundSummary{Total: len(refunds)}
	for _, r := range refunds {
		switch r.Status {
		case RefundStatePending:
			out.Pending++
		case RefundStateSubmitted:
			out.Submitted++
			if r.SettledAt == nil && now.Sub(r.RequestedAt) > StaleRefundAfter {
				out.StaleSubmitted++
			}
		case RefundStateSucceeded:
			out.Succeeded++
		case RefundStateFailed:
			out.Failed++
		case RefundStateManual:
			out.Manual++
		}
		if r.NeedsOperator(now, StaleRefundAfter) {
			out.NeedsAction++
		}
	}
	return out, nil
}

// TrialBalance returns every account's balance for the admin ledger view.
//
// A report an operator can read without knowing the schema: which accounts hold
// money, and how much. On an escrow marketplace the first question an operator
// asks is "how much are we holding, and for whom", and this is the answer.
func (s *PaymentService) TrialBalance(ctx context.Context) ([]domain.TrialBalanceRow, error) {
	if s.ledger == nil {
		return nil, domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so no trial balance can be produced")
	}
	return s.ledger.TrialBalance(ctx, s.payments.Pool())
}

// EscrowOutstanding is the number an operator asks for first: money held for
// other people right now.
//
// Before the ledger it could only be approximated by summing captured-but-not-
// released intents, which silently omits COD in transit, disputes, and any balance
// a refund has already drawn down.
func (s *PaymentService) EscrowOutstanding(ctx context.Context) (float64, error) {
	if s.ledger == nil {
		return 0, domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so escrow cannot be reported")
	}
	return s.ledger.EscrowOutstanding(ctx, s.payments.Pool())
}

// LedgerReconciliation exposes the drift report on demand, not only on the daily
// schedule.
//
// A scheduled check nobody can look at on demand is a check whose findings only
// exist in a log line. This is the endpoint that turns it into something an
// operator can act on.
func (s *PaymentService) LedgerReconciliation(ctx context.Context) (*domain.LedgerReconciliation, error) {
	if s.ledger == nil {
		return nil, domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so no reconciliation can be produced")
	}
	return s.ledger.ReconcileAll(ctx)
}

// slogRefundStatus reports a failure to record a refund's outcome.
//
// Loud, and specifically not returned to the caller: the refund row already
// exists and reconciliation will find it, whereas an error returned here would
// tell the caller the refund failed when the provider may have performed it --
// and the natural response to that is a retry.
func slogRefundStatus(ctx context.Context, id, want string, err error) {
	slog.Error("could not record refund outcome; reconciliation will resolve it",
		"refund_id", id, "want_status", want, "error", err.Error())
}

func newRefundID() string { return uuid.NewString() }

// refundDescription is a human-readable summary for the admin queue.
func refundDescription(r *repository.Refund) string {
	return fmt.Sprintf("%s Rp%.0f (%s)", r.Gateway, r.Amount, r.Status)
}

// isTerminal reports whether a refund is in a state that will not change on its
// own. Reconciliation only needs to poll the non-terminal ones.
func isTerminal(status string) bool {
	switch status {
	case RefundStateSucceeded, RefundStateFailed, RefundStateManual:
		return true
	}
	return false
}

// refundNeedsGatewayPoll reports whether a refund's outcome is still owned by the
// provider. This is the set reconciliation queries.
func refundNeedsGatewayPoll(status string) bool {
	return status == RefundStatePending || status == RefundStateSubmitted
}

// marshalRefundRaw converts the provider's response for storage.
func marshalRefundRaw(raw map[string]any) []byte {
	if len(raw) == 0 {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return b
}

// refundReasonForLog trims a reason for a log line; an operator-supplied string
// can be long and is not worth a multi-line log entry.
func refundReasonForLog(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 120 {
		return reason[:120] + "..."
	}
	return reason
}

// ReconcileRefunds polls the provider for refunds whose outcome it still owns,
// and settles the ones that have moved.
//
// This is the piece that makes the async refund honest. A refund the provider
// accepted stays `submitted` until somebody asks, and nobody was asking: the
// submit path returns as soon as the provider takes it, and without this job a
// refund that completed at the gateway would sit pending here forever. An
// operator looking at that queue would see refunds stuck at "submitted" with no
// way to tell which of them actually paid out -- which is the exact ambiguity
// that made refunds need a `manual` state in the first place.
//
// The provider is asked, not inferred. A refund submitted here and absent from
// the provider's list has been rejected or lost, and is marked failed rather than
// left pending, so the queue drains.
//
// Returns the counts so the worker's log line is meaningful.
func (s *PaymentService) ReconcileRefunds(ctx context.Context, limit int) (succeeded, failed, manual int, err error) {
	pending, err := s.payments.RefundsAwaitingGateway(ctx, limit)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, rf := range pending {
		switch rc := s.reconcileOneRefund(ctx, rf); rc {
		case RefundStateSucceeded:
			succeeded++
		case RefundStateFailed:
			failed++
		case RefundStateManual:
			manual++
		}
	}
	return succeeded, failed, manual, nil
}

// reconcileOneRefund resolves a single refund against the provider and records
// the outcome. Returns the state it moved to, or "" if nothing changed.
func (s *PaymentService) reconcileOneRefund(ctx context.Context, rf *repository.Refund) string {
	if !refundNeedsGatewayPoll(rf.Status) {
		return ""
	}

	// The provider's status endpoint is keyed on the CHARGE, and the refunds table
	// stores the REFUND's own id in gateway_ref. They are different identifiers and
	// conflating them was a bug: submitting overwrote the charge reference with
	// the refund id, so polling asked about a "transaction" that does not exist
	// and every reconciled refund came back not-found and was marked failed.
	//
	// The charge reference lives on the intent, which is where it belongs.
	intent, err := s.payments.IntentByOrder(ctx, rf.OrderID)
	if err != nil {
		return "" // transient; the next run retries
	}
	if chargeReference(intent) == "" {
		s.markRefundManual(ctx, rf.ID, "no gateway charge reference; an operator must transfer the money")
		return RefundStateManual
	}

	known, polled, err := s.pollProviderRefunds(ctx, rf, intent)
	if err != nil {
		return "" // transient; the next run retries
	}
	if !polled {
		// Either there is no charge reference, or the adapter cannot report
		// status. Both are already recorded (the second as manual) and neither
		// changes here.
		return ""
	}
	return s.classifyProviderRefunds(ctx, rf, intent, known)
}

// pollProviderRefunds asks the gateway what it thinks happened to this charge.
//
// Extracted so the identifier it sends is testable without a database. The
// argument is `chargeReference(intent)` and nothing else, and a mutation that
// passed the refund's own id here -- which 404s, so every reconciled refund comes
// back absent and gets marked failed -- passed every test while this logic was
// inline in a function that also required a live repository.
//
// The `polled` result is explicit rather than inferred from a nil slice, because
// a gateway that legitimately has no refunds for a charge returns an EMPTY list
// and inferring "not polled" from that would silently skip a refund that exists.
func (s *PaymentService) pollProviderRefunds(
	ctx context.Context, rf *repository.Refund, intent *domain.PaymentIntent,
) (statuses []payments.GatewayRefund, polled bool, err error) {
	if chargeReference(intent) == "" {
		return nil, false, nil
	}
	gw, gerr := s.gatewayFor(rf.Gateway)
	if gerr != nil {
		// The adapter is down. Leave the refund as it is: a transient
		// configuration problem is not a reason to tell an operator this buyer
		// needs manual intervention.
		return nil, false, nil
	}
	provider, ok := gw.(payments.RefundStatusProvider)
	if !ok {
		s.markRefundManual(ctx, rf.ID,
			"this gateway cannot report refund status; an operator must confirm completion")
		return nil, false, nil
	}
	// Addressed to the CHARGE. The refunds table stores this refund's own
	// provider id in its similarly named column, and the two must not be confused.
	statuses, err = provider.RefundStatus(ctx, chargeReference(intent))
	if err != nil {
		return nil, false, err
	}
	return statuses, true, nil
}

// classifyProviderRefunds turns the provider's list into an outcome.
//
// Split out from reconcileOneRefund, which does the I/O, so that the DECISION --
// which provider refund is ours, and what state that puts us in -- is a pure
// function of (refund, intent, provider list) and can be tested without a
// database or a network.
//
// That split is not cosmetic. Two of the mutations applied to the combined
// version survived: polling the refund id instead of the charge id, and
// treating acceptance as settlement. Both are real money bugs, and both were
// invisible because the decision was buried behind three layers of I/O. A
// decision that cannot be tested without a database is a decision nobody will
// test.
func (s *PaymentService) classifyProviderRefunds(
	ctx context.Context, rf *repository.Refund, intent *domain.PaymentIntent, known []payments.GatewayRefund,
) string {
	match, ambiguous := matchProviderRefund(known, rf)
	if ambiguous {
		// More than one refund of the same amount and the provider gave us no
		// refund id to tell them apart. Picking one would settle the wrong refund,
		// so this needs a human.
		s.markRefundManual(ctx, rf.ID,
			fmt.Sprintf("the gateway lists %d refunds of Rp%.0f and gave no refund id to "+
				"tell them apart; an operator must confirm which one this is",
				len(known), rf.Amount))
		return RefundStateManual
	}

	if match == nil {
		// We submitted a refund the provider does not know about. It was rejected
		// or lost, and it will never complete on its own. Failing it frees the
		// queue and tells an operator the buyer is still owed.
		s.markRefundFailed(ctx, rf.ID,
			"the gateway does not list this refund; it was rejected or lost and no money moved")
		return RefundStateFailed
	}

	switch match.Status {
	case payments.RefundStatusSucceeded:
		s.settleReconciledRefund(ctx, rf, intent, match)
		return RefundStateSucceeded
	case payments.RefundStatusManual:
		s.markRefundManual(ctx, rf.ID,
			"the gateway reports this refund needs manual completion: "+match.Reason)
		return RefundStateManual
	default:
		// Still pending at the provider. Nothing to do; the next run asks again.
		// NOT a settlement: the provider has accepted the refund and will move the
		// money later. Booking it now would report money we have not sent.
		return ""
	}
}

// gatewayRefundInput builds the provider request for a refund.
//
// A function so the two things that must not be confused -- the CHARGE being
// refunded, and the refund itself -- are assembled in one visible place.
func gatewayRefundInput(intent *domain.PaymentIntent, plan *refundPlan, reason string) payments.RefundInput {
	return payments.RefundInput{
		// The CHARGE reference, resolved through chargeReference so the submit and
		// reconcile paths cannot disagree about which identifier this is.
		Reference: chargeReference(intent),
		Amount:    plan.Amount,
		Currency:  intent.Currency,
		Reason:    reason,
		IdempotencyKey: "refund:" + intent.ID + ":" +
			strconv.FormatFloat(math.Round(plan.Amount), 'f', 0, 64),
	}
}

// chargeReference returns the provider's identifier for the CHARGE, which is what
// both the refund endpoint and the status endpoint are addressed to.
//
// One function, used by both paths, because the two identifiers are the same
// shape and live in similarly named places: `payment_intents.gateway_ref` is the
// charge, and `refunds.gateway_ref` is this refund's own id. Getting them
// backwards 404s every call, and the submit path and the reconcile path were
// written far enough apart that a mutation in one was invisible to tests on the
// other.
//
// Midtrans accepts the charge id in the form `tx-<id>` and requires the channel
// prefix to be stripped, so a stored value like `qris-abc` is normalised to `abc`.
func chargeReference(intent *domain.PaymentIntent) string {
	ref := strings.TrimSpace(intent.GatewayRef)
	if ref == "" {
		return ""
	}
	// Strip a channel prefix only when what follows looks like the provider's own
	// id, so a reference that legitimately contains a hyphen is not truncated.
	if i := strings.Index(ref, "-"); i > 0 {
		head := strings.ToLower(ref[:i])
		for _, p := range chargeChannelPrefixes {
			if head == p {
				return ref[i+1:]
			}
		}
	}
	if i := strings.Index(ref, "_"); i > 0 {
		head := strings.ToLower(ref[:i])
		for _, p := range chargeChannelPrefixes {
			if head == p {
				return ref[i+1:]
			}
		}
	}
	return ref
}

// chargeChannelPrefixes are the transaction-id channel prefixes Midtrans uses.
var chargeChannelPrefixes = []string{
	"va", "qris", "gop", "ovo", "dana", "linkaja", "shopeepay",
	"indomaret", "alfamart", "bsi", "bni", "bca", "bri", "mandiri", "cc", "tx",
}

// refundOutcome is what a gateway call means for the refund, as a decision with
// no side effects.
//
// The important one: RefundStatusPending is NOT a settlement. The provider has
// accepted the refund and will move the money during its settlement window.
// Treating acceptance as completion tells a buyer their money is back while the
// provider still holds it, and it also books a ledger entry for an outflow that
// has not happened.
type refundOutcome struct {
	status string
	// settle is true only when the provider has confirmed the money moved.
	settle bool
}

func classifyGatewayResult(res *payments.RefundResult) refundOutcome {
	if res == nil {
		return refundOutcome{status: RefundStateManual}
	}
	switch res.Status {
	case payments.RefundStatusSucceeded:
		return refundOutcome{status: RefundStateSucceeded, settle: true}
	case payments.RefundStatusPending:
		// Accepted, not settled. Explicitly NOT settle.
		return refundOutcome{status: RefundStateSubmitted, settle: false}
	case payments.RefundStatusManual:
		return refundOutcome{status: RefundStateManual}
	default:
		return refundOutcome{status: RefundStateFailed}
	}
}

// matchProviderRefund finds this refund among the provider's list for a charge.
//
// Matching is on the provider's refund id, which is exact. The amount is only a
// fallback, and only when it is UNAMBIGUOUS: a charge can have several refunds
// of the same amount, and picking the first of those settles the wrong one.
func matchProviderRefund(known []payments.GatewayRefund, rf *repository.Refund) (*payments.GatewayRefund, bool) {
	if rf.GatewayRef != "" {
		for i := range known {
			if known[i].ProviderRef == rf.GatewayRef {
				return &known[i], false
			}
		}
	}
	var byAmount []*payments.GatewayRefund
	for i := range known {
		if known[i].Amount == rf.Amount {
			byAmount = append(byAmount, &known[i])
		}
	}
	switch len(byAmount) {
	case 0:
		return nil, false
	case 1:
		return byAmount[0], false
	default:
		return nil, true
	}
}

// settleReconciledRefund applies the internal effect of a refund the provider
// confirmed during reconciliation.
//
// Reuses the same transaction as the submit-time completion, because the money
// movements and the ledger journal must be exactly the same operation whether the
// provider answered immediately or three days later. A second implementation here
// would be how a refund settled by the job and one settled by the click diverge.
func (s *PaymentService) settleReconciledRefund(
	ctx context.Context, rf *repository.Refund, intent *domain.PaymentIntent, match *payments.GatewayRefund,
) {
	plan := &refundPlan{Amount: rf.Amount}
	reason := rf.Reason
	if reason == "" {
		reason = "gateway reconciliation"
	}
	res := &payments.RefundResult{
		ProviderRef: match.ProviderRef,
		Status:      match.Status,
		Raw:         map[string]any{"source": "reconciliation", "reason": match.Reason},
	}
	if err := s.completeRefund(ctx, rf, intent, plan, res, reason, true); err != nil {
		slogRefundStatus(ctx, rf.ID, RefundStateSucceeded, err)
	}
}
