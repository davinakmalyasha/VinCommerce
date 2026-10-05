package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/stream"
)

// platformWalletID is the internal wallet that accumulates commission income.
const platformWalletID = "00000000-0000-0000-0000-000000000001"

// PaymentService implements the escrow payment lifecycle.
type PaymentService struct {
	payments        *repository.PaymentRepository
	orders          *repository.OrderRepository
	users           *repository.UserRepository
	gateways        map[string]payments.Gateway // adapters by name ("sandbox", "midtrans", ...)
	defaultGW       string                      // gateway used for generic bank_transfer/e_wallet methods
	baseURL         string
	broker          *stream.Broker
	notifications   *NotificationService
	mailer          *mail.Client
	webURL          string
	sandboxAutoSend bool // dev-only: instantly mark payouts sent (simulated transfer)
	payoutGuard     func(ctx context.Context, userID string) error
	disputes        *repository.DisputeRepository
	// ledger posts the double-entry journal alongside every wallet movement.
	//
	// Optional, and nil is tolerated, because a nil ledger must not stop a
	// payment from working. But the tolerance is the reason every posting site
	// logs loudly on skip: silently not accounting for a capture is the exact
	// defect this ledger was built to end, and reintroducing it as a "safe"
	// fallback would be worse than the original because it would look deliberate.
	ledger *LedgerService
	// statuses records refund state transitions. An interface rather than a
	// direct repository call so the DECISION of which transition to make is
	// testable without a database; see refundStatusWriter.
	statuses refundStatusWriter
	// refunds looks up a refund already recorded for a provider notification, so
	// a replayed webhook is a no-op. See refundLookup -- the reason this is an
	// interface is that the guard was otherwise untestable.
	refunds refundLookup
	// payoutLagDays is the platform default hold before a seller's money becomes
	// withdrawable. Read by the reservation-release job, so a per-seller override
	// configured here would be overridden by a hardcoded 7 in that query -- which
	// is why the value is a field and not a constant, and why the job reads it
	// rather than carrying its own.
	payoutLagDays int
}

// NewPaymentService wires the payment engine over one or more gateways.
// primary selects the adapter used for generic card-less methods.
func NewPaymentService(payRepo *repository.PaymentRepository, orders *repository.OrderRepository, gateways []payments.Gateway, primary, baseURL string) *PaymentService {
	reg := make(map[string]payments.Gateway, len(gateways))
	for _, g := range gateways {
		reg[g.Name()] = g
	}
	if primary == "" {
		primary = "sandbox"
	}
	return &PaymentService{
		payments: payRepo, orders: orders, gateways: reg, defaultGW: primary, baseURL: baseURL,
		statuses: paymentRefundStatuses{payments: payRepo},
		// Wired here, not lazily. This lookup used to be left nil in the
		// constructor, and the refund path fell back to building the repository
		// adapter itself when it found nil -- so the only path any test exercised
		// was a stub that production never runs, and the production path was the
		// one nothing covered. One constructor, one implementation: whatever the
		// tests drive is what ships.
		refunds: paymentRefundLookup{payments: payRepo},
	}
}

// SetUsers enables buyer lookup for transactional emails.
func (s *PaymentService) SetUsers(u *repository.UserRepository) { s.users = u }

// SetDisputes lets a money-moving dispute decision claim the dispute row
// inside its own transaction, so the claim and the wallet movements commit
// or roll back together.
func (s *PaymentService) SetDisputes(d *repository.DisputeRepository) { s.disputes = d }

// SetLedger attaches the double-entry ledger so every money movement posts a
// journal in the SAME transaction as the wallet writes it in.
//
// Same transaction, not adjacent to it, and that is the whole point: a journal
// committed a moment after the wallet credit is a window in which a crash leaves
// the two disagreeing, and nothing in the system would notice until the
// reconciliation job ran days later. Inside the transaction, both land or
// neither does.
func (s *PaymentService) SetLedger(l *LedgerService) { s.ledger = l }

// SetMailer enables transactional payment emails.
func (s *PaymentService) SetMailer(m *mail.Client, webURL string) { s.mailer = m; s.webURL = webURL }

// SetBroker attaches the realtime event broker (optional).
func (s *PaymentService) SetBroker(b *stream.Broker) { s.broker = b }

// SetNotificationService persists payment events as in-app notifications.
func (s *PaymentService) SetNotificationService(n *NotificationService) { s.notifications = n }

// SetSandboxAutoSend enables the dev-only instant payout simulation.
// Must never be enabled in production.
func (s *PaymentService) SetSandboxAutoSend(v bool) { s.sandboxAutoSend = v }

// PayoutGuard validates that a user may withdraw (e.g. KYC approved +
// store active). Wired at startup; nil disables the check.
func (s *PaymentService) SetPayoutGuard(g func(ctx context.Context, userID string) error) {
	s.payoutGuard = g
}

// CreateIntentInput for initiating a payment.
type CreateIntentInput struct {
	OrderID        string
	BuyerID        string
	Method         string
	IdempotencyKey string
}

// gatewayFor resolves the adapter for a payment method.
// "midtrans_snap" routes to the Midtrans adapter; everything generic
// (bank_transfer, e_wallet) uses the primary configured gateway.
func (s *PaymentService) gatewayFor(method string) (payments.Gateway, error) {
	if method == "midtrans_snap" {
		g, ok := s.gateways["midtrans"]
		if !ok {
			return nil, domain.E(domain.KindInvalid, "GATEWAY_UNAVAILABLE",
				"Midtrans payments are not configured on this server")
		}
		return g, nil
	}
	g, ok := s.gateways[s.defaultGW]
	if !ok {
		g, ok = s.gateways["sandbox"]
	}
	if !ok {
		return nil, domain.E(domain.KindInternal, "NO_GATEWAY", "no payment gateway configured")
	}
	return g, nil
}

// InitiatePayment creates the gateway charge and the escrow intent.
// Methods: bank_transfer, e_wallet (primary gateway); midtrans_snap (Snap);
// wallet (buyer balance, instant); cod.
func (s *PaymentService) InitiatePayment(ctx context.Context, in CreateIntentInput) (*domain.PaymentIntent, *payments.GatewayPayment, error) {
	order, err := s.orders.ByID(ctx, in.OrderID)
	if err != nil {
		return nil, nil, err
	}
	if order.BuyerID != in.BuyerID {
		return nil, nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if order.Status != domain.OrderPending {
		return nil, nil, domain.E(domain.KindConflict, "ORDER_NOT_PENDING", "only pending orders can be paid")
	}

	key := in.IdempotencyKey
	if key == "" {
		key = "intent-" + uuid.NewString()
	}

	// Wallet balance payment: instant capture, no gateway.
	// Debit, intent creation and capture run in ONE transaction.
	if in.Method == "wallet" {
		tx, err := s.orders.Begin(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer tx.Rollback(ctx)
		if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.BuyerID, "debit", "order_payment", order.TotalAmount, order.ID); err != nil {
			return nil, nil, err
		}
		intent := &domain.PaymentIntent{
			ID: uuid.NewString(), OrderID: order.ID, BuyerID: in.BuyerID,
			Amount: order.TotalAmount, Currency: order.Currency, Status: domain.IntentInitiated,
			Gateway: "internal", Method: "wallet", IdempotencyKey: key,
		}
		if err := s.payments.CreateIntentTx(ctx, tx.PgTx(), intent); err != nil {
			return nil, nil, err
		}
		if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
			[]string{domain.IntentInitiated}, domain.IntentCaptured); err != nil {
			return nil, nil, err
		}
		if err := tx.ConsumeReservation(ctx, order.ID); err != nil {
			return nil, nil, err
		}
		if err := tx.AddEvent(ctx, &domain.OrderEvent{
			OrderID: order.ID, FromStatus: domain.OrderPending, ToStatus: domain.OrderPaid,
			ActorID: &in.BuyerID, Note: "payment captured (escrow held)",
		}); err != nil {
			return nil, nil, err
		}
		if err := tx.SetPaymentStatus(ctx, order.ID, domain.PaymentPaid); err != nil {
			return nil, nil, err
		}
		if err := tx.SetStatus(ctx, order.ID, domain.OrderPaid); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		s.publish(ctx, order.ID, domain.OrderPending, domain.OrderPaid, "payment captured (escrow held)")
		s.emailFor(ctx, order.ID, "order_paid", "Pembayaran diterima — VinCommerce",
			map[string]any{"Total": fmt.Sprintf("Rp %.0f", intent.Amount)})
		return intent, &payments.GatewayPayment{Reference: "wallet", Status: "paid"}, nil
	}

	// COD: payment happens on delivery, but fulfillment must start now.
	// The order moves pendingâ†’paid with payment_status=pending (obligation
	// acknowledged, cash not yet collected) and the reservation is consumed
	// so the sweeper doesn't cancel it. CaptureCOD finalizes money state at
	// delivery confirmation.
	if in.Method == "cod" {
		intent := &domain.PaymentIntent{
			ID: uuid.NewString(), OrderID: order.ID, BuyerID: in.BuyerID,
			Amount: order.TotalAmount, Currency: order.Currency, Status: domain.IntentInitiated,
			Gateway: "cod", Method: "cod", IdempotencyKey: key,
		}
		tx, err := s.orders.Begin(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer tx.Rollback(ctx)
		if err := s.payments.CreateIntentTx(ctx, tx.PgTx(), intent); err != nil {
			return nil, nil, err
		}
		if err := tx.SetStatusGuarded(ctx, order.ID, domain.OrderPending, domain.OrderPaid); err != nil {
			return nil, nil, err
		}
		if err := tx.ConsumeReservation(ctx, order.ID); err != nil {
			return nil, nil, err
		}
		if err := tx.AddEvent(ctx, &domain.OrderEvent{
			OrderID: order.ID, FromStatus: domain.OrderPending, ToStatus: domain.OrderPaid,
			ActorID: &in.BuyerID, Note: "COD — bayar tunai saat barang diterima",
		}); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		s.publish(ctx, order.ID, domain.OrderPending, domain.OrderPaid, "COD — menunggu pelunasan saat diterima")
		return intent, &payments.GatewayPayment{Reference: "cod", Status: "pending"}, nil
	}

	gateway, err := s.gatewayFor(in.Method)
	if err != nil {
		return nil, nil, err
	}

	// Redirect buyers back to the storefront order page after checkout.
	finishBase := s.webURL
	if finishBase == "" {
		finishBase = s.baseURL
	}

	gwPayment, err := gateway.CreatePayment(ctx, payments.CreatePaymentInput{
		OrderID:     order.ID,
		OrderNumber: order.OrderNumber,
		Amount:      order.TotalAmount,
		Currency:    order.Currency,
		Idempotency: key,
		ReturnURL:   finishBase + "/orders/" + order.ID,
	})
	if err != nil {
		return nil, nil, domain.Wrap(domain.KindInternal, "GATEWAY_ERROR", "payment gateway unavailable", err)
	}

	intent := &domain.PaymentIntent{
		ID:             uuid.NewString(),
		OrderID:        order.ID,
		BuyerID:        in.BuyerID,
		Amount:         order.TotalAmount,
		Currency:       order.Currency,
		Status:         domain.IntentInitiated,
		Gateway:        gateway.Name(),
		GatewayRef:     gwPayment.Reference,
		SnapToken:      gwPayment.Token,
		RedirectURL:    gwPayment.RedirectURL,
		Method:         in.Method,
		IdempotencyKey: key,
	}
	if err := s.payments.CreateIntent(ctx, intent); err != nil {
		// Idempotent replay: hand back the stored intent instead of a conflict
		// so a retried request still receives its checkout token/URL.
		if domain.Is(err, domain.KindConflict, "IDEMPOTENCY_REPLAY") || domain.Is(err, domain.KindConflict, "INTENT_EXISTS") {
			existing, ferr := s.payments.IntentByKey(ctx, key)
			if ferr != nil {
				return nil, nil, err
			}
			return existing, &payments.GatewayPayment{
				Reference:   existing.GatewayRef,
				Token:       existing.SnapToken,
				RedirectURL: existing.RedirectURL,
				Status:      "pending",
			}, nil
		}
		return nil, nil, err
	}
	return intent, gwPayment, nil
}

// CaptureCOD finalizes payment when the buyer confirms delivery on a COD order.
// COD capture must NOT touch order status — the order is already delivered;
// only the money state (intent â†’ captured, payment_status â†’ paid) moves.
func (s *PaymentService) CaptureCOD(ctx context.Context, orderID string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Method != "cod" || intent.Status != domain.IntentInitiated {
		return nil
	}
	return s.captureIntentOnly(ctx, intent)
}

// captureIntentOnly flips an initiated intent to captured and marks the order
// payment_status=paid, leaving the order status untouched (used by COD where
// delivery has already happened).
//
// POSTS THE CAPTURE JOURNAL, which it did not.
//
// `onPaid` posts one and this did not, and `onPaid` is only reachable from
// `HandleWebhook` -- i.e. only for gateway payments. COD capture happens HERE, at
// delivery, through a different door. So no COD order was ever booked: the buyer
// paid cash to a courier, `escrow_held` was never debited, and `cod_receivable`
// -- the account seeded in 00043 precisely because that cash is the courier's and
// not a gateway's -- sat at zero forever.
//
// The consequence is not a misclassified account, it is money from nowhere: the
// books showed Rp0 of captured COD against Rp100,000 of goods delivered, and the
// reconciliation had nothing to disagree with because the journal was never
// written. `cod_receivable` being dead was the symptom.
//
// The entries are the same `captureEntries` the gateway path uses, which is the
// point: the whole reason that helper branches on the method is that COD cash
// belongs to the courier for several days. `TestCODCaptureGoesToTheCourierNotTheGateway`
// pinned the HELPER and therefore passed while the COD path never called it -- a
// test of the classifier, not of the wiring. That is the fifth instance of this
// pattern in this workstream.
//
// Posted STRICTLY, like every other write in this transaction: this is one event,
// and `postLedger` swallowing the failure is part of why the omission went
// unnoticed.
func (s *PaymentService) captureIntentOnly(ctx context.Context, intent *domain.PaymentIntent) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
		[]string{domain.IntentInitiated}, domain.IntentCaptured); err != nil {
		return err
	}
	if err := tx.ConsumeReservation(ctx, intent.OrderID); err != nil {
		return err
	}
	if err := tx.SetPaymentStatus(ctx, intent.OrderID, domain.PaymentPaid); err != nil {
		return err
	}
	if err := s.postLedgerStrict(ctx, tx.Querier(), JournalSpec{
		IdempotencyKey: "capture:" + intent.ID,
		TxType:         TxTypePayment,
		RefType:        "payment_intent",
		RefID:          intent.ID,
		Note:           "COD collected on delivery; funds held in escrow",
		Entries: withMeta(captureEntries(intent.Amount, intent.Method), map[string]any{
			"order_id": intent.OrderID, "method": intent.Method,
		}),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publish(ctx, intent.OrderID, "", "", "pembayaran COD tercatat")
	s.emailFor(ctx, intent.OrderID, "order_paid", "Pembayaran diterima — VinCommerce",
		map[string]any{"Total": fmt.Sprintf("Rp %.0f", intent.Amount)})
	return nil
}

// HandleWebhook processes a gateway event (idempotent). Events are routed to
// the adapter named in the URL, so sandbox and Midtrans notifications coexist.
func (s *PaymentService) HandleWebhook(ctx context.Context, gatewayName string, payload []byte, signature string) error {
	gateway, ok := s.gateways[gatewayName]
	if !ok {
		return domain.E(domain.KindInvalid, "UNKNOWN_GATEWAY", "unknown gateway")
	}
	ok, err := gateway.VerifyWebhook(ctx, payload, signature)
	if err != nil || !ok {
		return domain.E(domain.KindUnauthenticated, "BAD_SIGNATURE", "invalid webhook signature")
	}
	ev, err := gateway.ParseWebhook(payload)
	if err != nil {
		return domain.E(domain.KindInvalid, "BAD_WEBHOOK", "malformed webhook payload")
	}

	intent, err := s.payments.IntentByRef(ctx, gatewayName, ev.Reference)
	if err != nil {
		return err
	}

	// Remember the concrete channel the buyer actually paid with
	// (Midtrans reports it as payment_type: gopay, qris, kredivo, ...).
	if gatewayName == "midtrans" {
		if pt, ok := ev.Raw["payment_type"].(string); ok && pt != "" && pt != intent.Method {
			if err := s.payments.SetIntentMethod(ctx, intent.ID, pt); err != nil {
				return err
			}
			intent.Method = pt
		}
	}

	switch ev.Type {
	case payments.EventPaid:
		// Never capture when the provider amount is missing or disagrees with
		// the intent — a webhook without an amount must not move money.
		if ev.Amount <= 0 {
			return domain.E(domain.KindInvalid, "AMOUNT_REQUIRED",
				"webhook did not include a payable amount")
		}
		if absDiff(ev.Amount, intent.Amount) > 0.01 {
			return domain.E(domain.KindConflict, "AMOUNT_MISMATCH",
				fmt.Sprintf("webhook amount %.2f does not match intent amount %.2f", ev.Amount, intent.Amount))
		}
		return s.onPaid(ctx, intent)
	case payments.EventRefunded:
		return s.onRefunded(ctx, intent, intent.Amount, ev)
	case payments.EventPartiallyRefunded:
		// A partial refund carries the REFUNDED amount, not the charge total.
		// Routing it through onRefunded credited the whole order to the buyer:
		// a Rp 10 refund at the gateway paid out Rp 100.000.
		amount := ev.Amount
		if amount <= 0 {
			// Fail closed rather than guessing. Guessing "the whole thing" is
			// exactly the bug this branch exists to prevent.
			return domain.E(domain.KindInvalid, "REFUND_AMOUNT_REQUIRED",
				"partial refund notification did not include a refunded amount")
		}
		if amount > intent.Amount+0.01 {
			return domain.E(domain.KindConflict, "REFUND_EXCEEDS_CHARGE",
				fmt.Sprintf("refund %.2f exceeds the charged amount %.2f", amount, intent.Amount))
		}
		return s.onRefunded(ctx, intent, amount, ev)
	case payments.EventFailed:
		// Never downgrade financial state: once money has been captured (or
		// moved on) a late cancel/deny/expire notification must not mark the
		// intent failed. Surface for manual review instead.
		if intent.Status == domain.IntentCaptured || intent.Status == domain.IntentReleased ||
			intent.Status == domain.IntentRefunded || intent.Status == domain.IntentPartiallyRefunded {
			return nil
		}
		if err := s.payments.SetIntentStatus(ctx, intent.ID, domain.IntentFailed); err != nil {
			return err
		}
	}
	return nil
}

// absDiff is the absolute difference between two amounts.
//
// With whole-rupiah money the epsilon comparisons this used to guard --
// `absDiff(x, y) > 0.01`, `> 1.0` -- are no longer load-bearing. `> 1.0` in
// particular let a buyer mark a Rp1,000,000 order paid by reporting 999,999.00,
// and on a 100%-discount order the minimum accepted claim was Rp0.01. Those
// should become exact equality, which is the real fix; absDiff remains for the
// few comparisons that are genuinely about tolerance rather than equality.
// (see absDiff above)

// moneyRound is defined in money.go and rounds to WHOLE rupiah.
//
// It used to live here and round to two decimals, on the reasoning that "the
// schema stores NUMERIC(14,2) so round to 2dp". That reasoning is what caused
// the gateway mismatch: the column can carry sen, the currency cannot, and
// `payments/midtrans.go` charges `int64(math.Round(total))`. A 3% coupon on a
// Rp12,345.67 cart produced a stored total of Rp11,975.30 and a Midtrans charge
// of Rp11,975; the webhook compared the two with a 1-sen tolerance, rejected the
// notification as AMOUNT_MISMATCH, and the expired-order sweeper then cancelled
// the order at T+30min with the buyer's money already debited and settled to the
// platform's bank.
//
// Rounding to whole rupiah everywhere closes that class of bug at the source:
// there is no longer a value the application stores that the gateway cannot
// charge. The NUMERIC(14,2) columns still hold whole rupiah fine; tightening
// them to NUMERIC(14,0) is a separate schema change.

// refundedTotal sums the refund legs already written to the ledger for an
// order.
//
// The intent status cannot answer this: a `partially_refunded` intent does not
// record how much has already gone back, so two individually-valid partial
// refunds could together exceed the charge. The ledger is the only source
// that is guaranteed to agree with the money that actually moved.
//
// `excludeRefundID` is the refund row this plan is FOR, and it is left out of the
// total. A settlement re-derives the plan after the reserve step has already
// committed its row as `submitted`, and without the exclusion the row is counted
// against itself -- so a full refund sees `already = charge`, computes
// `remaining = 0` and refuses itself. Pass nil when no row of ours is in flight
// yet (the reserve step itself).
//
// `q` must be the caller's open transaction, not the pool. See
// PaymentRepository.SumRefundedByOrder for why that distinction is the whole
// ballgame under concurrency.
func (s *PaymentService) refundedTotal(
	ctx context.Context, q repository.Querier, orderID string, excludeRefundID *string,
) (float64, error) {
	var total float64
	if err := s.payments.SumRefundedByOrder(ctx, q, orderID, excludeRefundID, &total); err != nil {
		return 0, err
	}
	return moneyRound(total), nil
}

// refundPlan is the derived, validated outcome of a refund, computed once and
// applied identically by every refund entry point.
type refundPlan struct {
	// Remaining is the un-refunded balance of the charge after everything
	// already written to the ledger, read INSIDE the transaction and after the
	// intent row lock.
	Remaining float64
	// WasReleased reports whether escrow was ever paid out to the seller. If it
	// was, the refund must claw the seller's net and the platform's fee back;
	// if not, there is nothing to claw back because the money never left.
	WasReleased bool
	// Fee and SellerAmount are the split actually applied to this intent, which
	// is what makes the reversal exact rather than recomputed from today's
	// configured rate.
	Fee, SellerAmount float64
	// RefundFee and RefundSeller are THIS refund's share of those two legs,
	// derived so that RefundFee + RefundSeller == amount by construction.
	RefundFee, RefundSeller float64
	// Partial reports whether this refund leaves a balance.
	Partial bool
	// NextStatus is the intent status this refund moves to.
	NextStatus string
	// Amount is the amount being refunded.
	Amount float64
}

// buildRefundPlan derives and validates a refund against the ledger.
//
// Every invariant that makes a refund safe lives here, once:
//
//   - the cumulative refunded total is read AFTER the intent row lock, so a
//     concurrent refund cannot interleave between the read and the write
//   - the total may never exceed what was charged
//   - the two reversal legs sum to the refund amount exactly
//   - nothing is clawed back if escrow was never released
//   - the row being settled is excluded from its own total
//
// The previous code had these spread across RefundOrder and RefundReturn, and
// RefundReturn had NONE of them -- which is why a second return refund on the
// same order credited the buyer again with no cap, no commission reversal, and
// no debit anywhere.
func (s *PaymentService) buildRefundPlan(
	ctx context.Context, q repository.Querier, intent *domain.PaymentIntent, amount float64,
	excludeRefundID *string,
) (*refundPlan, error) {
	already, err := s.refundedTotal(ctx, q, intent.OrderID, excludeRefundID)
	if err != nil {
		return nil, err
	}
	remaining := moneyRound(intent.Amount - already)
	if remaining <= 0 {
		return nil, domain.E(domain.KindConflict, "ALREADY_REFUNDED",
			"this order has already been fully refunded")
	}
	if amount <= 0 {
		amount = remaining
	}
	// Exact comparison, not an epsilon. With whole-rupiah money the epsilon was
	// only ever a workaround for float drift, and a tolerance of one rupiah on a
	// buyer's refund is one rupiah of money that does not exist.
	if amount > remaining {
		return nil, domain.E(domain.KindConflict, "REFUND_EXCEEDS_REMAINING",
			fmt.Sprintf("refund Rp%.0f exceeds the remaining refundable Rp%.0f of Rp%.0f charged",
				amount, remaining, intent.Amount))
	}

	wasReleased, err := s.escrowWasReleasedQuerier(ctx, q, intent.OrderID)
	if err != nil {
		return nil, err
	}

	// The fee ACTUALLY applied to this intent, so the reversal is exact.
	//
	// The old code fell back to the currently-active configured rate whenever
	// `fee == 0`, which conflated "not computed yet" with "genuinely 0%". A
	// promo-period order released at 0% has fee_amount = 0, so refunding it later
	// recomputed a fee at today's rate and then DEBITED that from the platform
	// wallet -- taking commission earned on other sellers' orders, or failing
	// the whole refund with INSUFFICIENT_BALANCE and leaving the buyer's return
	// stuck with no retry path. `commission_computed` is the correct predicate;
	// the snapshot columns are the correct source.
	fee := intent.FeeAmount
	sellerAmount := intent.SellerAmount
	if !intent.CommissionComputed {
		if cfg, ferr := s.payments.ActiveFeeTx(ctx, q); ferr == nil {
			fee, sellerAmount = repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)
		}
	}
	if fee < 0 {
		fee = 0
	}
	if sellerAmount < 0 {
		sellerAmount = 0
	}

	refundFee, refundSeller := 0.0, 0.0
	if wasReleased && intent.Amount > 0 {
		// Scale the fee to this refund's share of the charge, then make the
		// seller's leg the RESIDUAL so the two sum to `amount` by construction.
		// Rounding two independently-scaled values drifted by a sen per partial
		// refund, and the drift accumulates permanently in the wallet balances.
		ratio := amount / intent.Amount
		refundFee = moneyRound(fee * ratio)
		refundSeller = moneyRound(amount - refundFee)
		if refundSeller < 0 {
			refundSeller = 0
		}
		// Re-derive if the residual fell outside the leg, so the pair always
		// sums exactly. Only reachable through float edge cases, but a pair that
		// does not sum means money created or destroyed.
		if refundFee+refundSeller != amount {
			refundFee = moneyRound(amount - refundSeller)
		}
	}

	partial := amount != remaining
	next := domain.IntentPartiallyRefunded
	if !partial {
		next = domain.IntentRefunded
	}
	return &refundPlan{
		Remaining:    remaining,
		WasReleased:  wasReleased,
		Fee:          fee,
		SellerAmount: sellerAmount,
		RefundFee:    refundFee,
		RefundSeller: refundSeller,
		Partial:      partial,
		NextStatus:   next,
		Amount:       amount,
	}, nil
}

// escrowWasReleased reports whether this order's escrow was ever paid out to
// the seller, read from the ledger rather than the intent status. A
// partially_refunded intent has lost that bit of information.
//
// It takes a *repository.OrderTx rather than a pgx.Tx. This function is the only
// place in the service layer that named the pgx transaction type directly, and
// it was the last one: everything else reaches persistence through the OrderTx
// unit of work, which is what keeps the transaction boundary owned by the
// repository rather than by whoever happens to call it.
func (s *PaymentService) escrowWasReleased(ctx context.Context, tx *repository.OrderTx, orderID string) (bool, error) {
	return s.escrowWasReleasedQuerier(ctx, tx.Querier(), orderID)
}

// escrowWasReleasedQuerier is the Querier-taking form, so callers that already
// hold a transaction handle do not have to widen it back to a pgx.Tx.
func (s *PaymentService) escrowWasReleasedQuerier(ctx context.Context, q repository.Querier, orderID string) (bool, error) {
	var released bool
	if err := s.payments.HasEscrowRelease(ctx, q, orderID, &released); err != nil {
		return false, err
	}
	return released, nil
}

// onPaid captures escrow and moves the order to paid.
// All financial writes (intent status, reservation consumption, order status,
// payment status) happen inside ONE transaction so a crash can never leave a
// captured intent with held stock or an unpaid paid-order.
func (s *PaymentService) onPaid(ctx context.Context, intent *domain.PaymentIntent) error {
	if intent.Status == domain.IntentCaptured || intent.Status == domain.IntentReleased {
		return nil // idempotent replay
	}
	if intent.Status != domain.IntentInitiated {
		return domain.E(domain.KindConflict, "INTENT_STATE", "intent is not payable")
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Guarded transition inside the tx: concurrent webhooks/replays cannot
	// double-capture (the second one finds no row in 'initiated').
	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
		[]string{domain.IntentInitiated}, domain.IntentCaptured); err != nil {
		return err
	}
	if err := tx.ConsumeReservation(ctx, intent.OrderID); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: intent.OrderID, FromStatus: domain.OrderPending, ToStatus: domain.OrderPaid,
		ActorID: &intent.BuyerID, Note: "payment captured (escrow held)",
	}); err != nil {
		return err
	}
	// pendingâ†’paid ONLY. If the order moved on meanwhile (cancelled by the
	// payment-timeout sweeper, or already paid via another path), the whole
	// capture rolls back — a late webhook can never resurrect it.
	if err := tx.SetStatusGuarded(ctx, intent.OrderID, domain.OrderPending, domain.OrderPaid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || domain.Is(err, domain.KindConflict, "") || domain.Is(err, domain.KindNotFound, "") {
			return domain.E(domain.KindConflict, "ORDER_NOT_PENDING",
				"order is no longer awaiting payment; capture rejected")
		}
		return err
	}
	if err := tx.SetPaymentStatus(ctx, intent.OrderID, domain.PaymentPaid); err != nil {
		return err
	}
	// Capture journal, inside the capture transaction.
	//
	// CREDIT escrow_held, DEBIT a clearing account. Escrow is a LIABILITY -- money
	// we are holding for a buyer and a seller -- so receiving money INCREASES it,
	// on the credit side. Debiting it here would make escrow read as a negative
	// liability from the very first payment, and the release journal (which
	// correctly debits it) would then drive the balance further the wrong way.
	// The direction is easy to get backwards, and it balances either way, so
	// nothing complains; TestCaptureJournalBalancesAndEscrows asserts it.
	//
	// This step previously wrote nothing at all, which is why the books showed
	// Rp2,000 for a Rp100,000 order: the inflow was never recorded anywhere, so
	// there was nothing to balance the release against.
	//
	// COD is deliberately routed through cod_receivable instead of
	// gateway_clearing: the cash is held by the courier, not by a payment
	// gateway, and posting it to a gateway account would put the money in the
	// wrong place for the several days it spends there.
	//
	// TestCODCaptureGoesToTheCourierNotTheGateway pins this, and a mutation that
	// routes COD to gateway_clearing is caught by it.
	s.postLedger(ctx, tx.PgTx(), JournalSpec{
		IdempotencyKey: "capture:" + intent.ID,
		TxType:         TxTypePayment,
		RefType:        "payment_intent",
		RefID:          intent.ID,
		Note:           "payment captured; funds held in escrow",
		Entries: withMeta(captureEntries(intent.Amount, intent.Method), map[string]any{
			"order_id": intent.OrderID, "method": intent.Method,
		}),
	})
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publish(ctx, intent.OrderID, domain.OrderPending, domain.OrderPaid, "payment captured (escrow held)")
	s.emailFor(ctx, intent.OrderID, "order_paid", "Pembayaran diterima — VinCommerce",
		map[string]any{"Total": fmt.Sprintf("Rp %.0f", intent.Amount)})
	return nil
}

func (s *PaymentService) emailFor(ctx context.Context, orderID, templateName, subject string, data map[string]any) {
	if s.mailer == nil || s.users == nil {
		return
	}
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return
	}
	buyer, err := s.users.ByID(ctx, o.BuyerID)
	if err != nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Name"] = buyer.FullName
	data["OrderNumber"] = o.OrderNumber
	data["OrderURL"] = s.webURL + "/orders/" + o.ID
	_ = s.mailer.Send(ctx, buyer.Email, subject, templateName, data)
}

func (s *PaymentService) publish(ctx context.Context, orderID, from, to, message string) {
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return
	}
	if s.broker != nil {
		ev := stream.Event{
			Type: "order.status_changed", OrderID: orderID, OrderNumber: o.OrderNumber,
			FromStatus: from, ToStatus: to, Message: message, At: time.Now().UTC(),
		}
		_ = s.broker.Publish(ctx, o.BuyerID, ev)
		if o.SellerID != o.BuyerID {
			_ = s.broker.Publish(ctx, o.SellerID, ev)
		}
	}
	if s.notifications != nil {
		_ = s.notifications.Notify(ctx, o.BuyerID, "payment", "Pembayaran diterima",
			"Pesanan "+o.OrderNumber+" telah dibayar (dana dipegang escrow).",
			map[string]any{"order_id": orderID, "status": to})
		if o.SellerID != o.BuyerID {
			_ = s.notifications.Notify(ctx, o.SellerID, "payment", "Pesanan dibayar",
				"Pesanan "+o.OrderNumber+" telah dibayar, siap dikemas.",
				map[string]any{"order_id": orderID, "status": to})
		}
	}
}

// ReleaseEscrow transfers funds to the seller wallet (on delivery completion),
// deducting the platform commission. The status flip, fee split and both
// wallet credits happen in ONE transaction — a crash can never release escrow
// without crediting the seller.
func (s *PaymentService) ReleaseEscrow(ctx context.Context, orderID string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	// Guarded by SQL below too; only 'captured' may be released so repeated
	// calls cannot double-credit the seller.
	if intent.Status != domain.IntentCaptured {
		return domain.E(domain.KindConflict, "ESCROW_NOT_HELD", "escrow is not held for this order")
	}
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}

	fee, sellerAmount := intent.Amount, 0.0
	if intent.FeeAmount > 0 {
		fee, sellerAmount = intent.FeeAmount, intent.SellerAmount
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	// Derive the split from the rate card SNAPSHOTTED AT CHARGE TIME.
	//
	// This used to read `platform_fees WHERE is_active` at release time, which
	// meant an admin changing the commission between the order and the delivery
	// changed what the seller was paid for a sale already quoted at the old
	// rate. On a Rp 5,000,000 order a 2% -> 10% change is Rp 400,000 taken from
	// a seller who never agreed to it, and the only way to find out was to diff
	// the release timestamp against the settings history.
	//
	// `commission_computed` is the correct predicate for "has this been derived",
	// not `fee_amount == 0` -- a 0% promotional rate produces a legitimate zero
	// fee, and using the amount as the flag made every 0% order recompute on
	// every release.
	if !intent.CommissionComputed {
		pct, fixed := intent.CommissionRatePct, intent.CommissionRateFixed
		if pct == 0 && fixed == 0 {
			// No snapshot (an intent predating migration 00042, or a missing rate
			// card at charge time). Fall back to the active rate, which is the
			// pre-existing behaviour and strictly better than a zero fee.
			cfg, ferr := s.payments.ActiveFeeTx(ctx, q)
			if ferr != nil {
				return ferr
			}
			pct, fixed = cfg.Pct, cfg.Fixed
		}
		fee, sellerAmount = repository.Commission(pct, fixed, intent.Amount)
		if err := s.payments.ApplyFeeTx(ctx, q, intent.ID, fee, sellerAmount); err != nil {
			return err
		}
		intent.FeeAmount, intent.SellerAmount = fee, sellerAmount
		intent.CommissionComputed = true
	}

	if err := s.payments.SetIntentStatusGuardedTx(ctx, q, intent.ID,
		[]string{domain.IntentCaptured}, domain.IntentReleased); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: order.Status, ToStatus: order.Status,
		ActorID: nil, Note: fmt.Sprintf("escrow released to seller (komisi platform Rp %.0f)", fee),
	}); err != nil {
		return err
	}

	// seller credited the net amount; platform fee recorded against the platform wallet.
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.SellerID, "credit", domain.TxReasonEscrowRelease, sellerAmount, orderID); err != nil {
		return err
	}
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), platformWalletID, "credit", "commission", fee, orderID); err != nil {
		return err
	}

	// Release journal, inside the release transaction and AFTER the commission
	// has been resolved -- it posts the numbers that were actually used, not the
	// ones guessed before the snapshot fell back.
	s.postLedger(ctx, q, JournalSpec{
		IdempotencyKey: "release:" + intent.ID,
		TxType:         TxTypeEscrowRelease,
		RefType:        "order",
		RefID:          orderID,
		Note:           fmt.Sprintf("escrow released; platform commission Rp %.0f", fee),
		Entries: withMeta(releaseEntries(order.SellerID, intent.Amount, sellerAmount, fee), map[string]any{
			"order_id": orderID, "seller_id": order.SellerID,
			"gross": intent.Amount, "fee": fee, "seller_net": sellerAmount,
		}),
	})

	// THE COD HOLD, in this transaction, for this one reason.
	//
	// This is the moment COD money first becomes withdrawable, so it is the only
	// place a hold can actually bite for COD. Holding it at `CaptureCOD` instead --
	// the obvious spot, since that is where the cash is collected -- would fail:
	// at capture the money is in `escrow_held`, the seller's `wallets.balance` is
	// zero, and `WalletHeldTxOn` would refuse with INSUFFICIENT_BALANCE on every
	// single COD order.
	//
	// WHAT IT PROTECTS AGAINST. COD capture assumes the buyer handed over the cash.
	// Sometimes they did not: the parcel comes back, the courier never collected,
	// and the order is refunded. That refund reverses the seller's NET out of their
	// wallet -- so a seller who withdrew after completion leaves nothing to reverse,
	// the refund fails on `balance >= amount`, and the buyer's money is stranded. The
	// hold keeps the net out of reach until the lag has passed.
	//
	// The SELLER'S NET, not the gross. A refused delivery does not take back the
	// platform's commission -- the seller did ship, and the platform did its work --
	// so holding the gross would freeze money the platform is entitled to keep and
	// make a bad COD look like a worse one.
	//
	// Inside the transaction on purpose: a separate one would leave a window between
	// "the seller is credited" and "the money is held" in which the seller could
	// withdraw the whole amount. That window is the fraud.
	if intent.Method == domain.MethodCOD && sellerAmount > 0 {
		if err := s.holdSellerFundsTx(ctx, q, order.SellerID, orderID, intent.ID,
			HoldCOD, "COD collected; awaiting the return window", sellerAmount); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.publish(ctx, orderID, order.Status, order.Status, "escrow released to seller")
	s.emailFor(ctx, orderID, "order_completed", "Pesanan selesai — dana escrow dilepas",
		map[string]any{"Total": fmt.Sprintf("Rp %.0f", sellerAmount)})
	return nil
}

// RefundOrder moves money back to the buyer.
//
// amount <= 0 means "refund the full charge". A partial refund is supported
// and is what a gateway partial_refund notification produces.
//
// Three defects this replaces:
//
//  1. It ignored order.Status entirely. On a CANCELLED order whose intent was
//     still captured, an admin could release the escrow (+seller) and then
//     refund it (-seller, +buyer) — the seller's net was zero but their wallet
//     balance and payout eligibility had both grown, and a withdrawal in
//     between turned the mint into real bank cash.
//
//  2. It debited the SELLER the gross amount after a release, where the seller
//     had only ever been credited the net (amount - fee). The platform kept its
//     commission on a fully refunded order and the seller paid the fee out of
//     unrelated balance.
//
//  3. It marked the intent `refunded` and the order `refunded` even for a
//     partial refund, so a second legitimate refund could never be applied.
func (s *PaymentService) RefundOrder(ctx context.Context, orderID string, reason string, refundToBuyer bool, amount float64) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	// partially_refunded MUST be accepted here, otherwise a partially-refunded
	// intent can never be refunded again - not for the remainder, not at all.
	// The first version excluded it, which made the partial-refund branches
	// below unreachable dead code and left a buyer who received a 30% gateway
	// refund permanently stuck at 30% with no admin path either.
	switch intent.Status {
	case domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded:
		// refundable
	default:
		return domain.E(domain.KindConflict, "NOT_REFUNDABLE",
			"only captured or released payments can be refunded")
	}
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}

	// Refuse to move money against a cancelled order. A cancellation that
	// happens after capture has to be reconciled explicitly by an operator,
	// not silently turned into a seller credit plus a buyer credit.
	if order.Status == domain.OrderCancelled {
		return domain.E(domain.KindConflict, "ORDER_CANCELLED",
			"order is cancelled; reconcile the escrow with an operator before refunding")
	}

	// Route through ExecuteRefund, which returns the money through the gateway and
	// records a durable `refunds` row.
	//
	// This used to credit a wallet directly. That is the behaviour being replaced:
	// a buyer who paid by QRIS or bank transfer was refunded in platform credit
	// they could not spend or withdraw, the money stayed with the platform, and
	// for Midtrans it was a terms-of-service breach.
	refund, rerr := s.ExecuteRefund(ctx, orderID, reason, refundToBuyer, amount, "")

	// The notification is sent whether or not the gateway confirmed, because the
	// two mean different things to the buyer and conflating them is its own bug:
	//
	//   succeeded  the money is on its way back to their card or account
	//   submitted  the provider accepted it and will settle it
	//   manual     an operator is handling it personally
	//
	// Saying "your refund is processed" when it is queued for a human is how a
	// support queue fills with buyers who were told the money had moved.
	switch {
	case rerr != nil:
		return rerr
	case refund == nil:
		return nil
	}
	s.notifyRefund(ctx, order, refund, reason)
	return nil
}

// notifyRefund tells the buyer what actually happened to their money.
func (s *PaymentService) notifyRefund(ctx context.Context, order *domain.Order, refund *repository.Refund, reason string) {
	if s.notifications == nil {
		return
	}
	var body string
	switch refund.Status {
	case RefundStateSucceeded:
		body = fmt.Sprintf("Refund Rp%.0f untuk pesanan %s telah dikirim ke metode pembayaran Anda.",
			refund.Amount, order.OrderNumber)
	case RefundStateManual:
		body = fmt.Sprintf("Refund Rp%.0f untuk pesanan %s sedang diproses manual oleh tim kami.",
			refund.Amount, order.OrderNumber)
	default:
		body = fmt.Sprintf("Refund Rp%.0f untuk pesanan %s sedang diproses oleh penyedia pembayaran.",
			refund.Amount, order.OrderNumber)
	}
	_ = s.notifications.Notify(ctx, order.BuyerID, "order", "Refund diproses", body,
		map[string]any{
			"order_id":   order.ID,
			"refund_id":  refund.ID,
			"refund_amt": refund.Amount,
			"status":     refund.Status,
		})
}

// RefundOrderInTx applies a refund inside a transaction the CALLER owns.
//
// It exists so the return path cannot be a second, weaker refund
// implementation. RefundReturn used to credit the buyer with no cumulative cap,
// no debit, no commission reversal, and a terminal intent status on the first
// item -- an unbounded mint from one admin click. There is now exactly one place
// where refund arithmetic happens.
//
// The caller's transaction is not committed here. RefundReturn marks the return
// claim refunded in the same transaction, so a failure rolls both back together
// and leaves the claim retryable rather than stranded.
func (s *PaymentService) RefundOrderInTx(
	ctx context.Context, tx *repository.OrderTx, orderID string, amount float64, reason string,
) error {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	return s.refundInTx(ctx, tx, order, intent.ID, amount, reason, true, false)
}

// refundInTx is the single implementation of refund money movement.
//
// The caller owns the transaction and the intent id. Inside, in this order:
//
//  1. lock the intent row                    (serialises concurrent refunds)
//  2. re-read the intent under the lock      (detect a concurrent transition)
//  3. read the cumulative refunded total     (from the ledger, inside the tx)
//  4. cap and derive the two reversal legs   (so they sum to `amount` exactly)
//  5. move the money and transition the state
//
// Steps 3 and 4 were previously outside the transaction and before the lock,
// which is why two concurrent partial refunds could each be individually valid
// and together refund more than was charged.
func (s *PaymentService) refundInTx(
	ctx context.Context, tx *repository.OrderTx, order *domain.Order, intentID string,
	amount float64, reason string, refundToBuyer bool, addEvent bool,
) error {
	orderID := order.ID
	q := tx.Querier()

	locked, err := s.payments.LockIntentForRefund(ctx, q, intentID)
	if err != nil {
		return err
	}
	// nil: the cap must see every refund that came BEFORE this one, and this
	// refund's own row does not exist yet -- it is written below, inside this same
	// transaction, once the plan is known to be valid.
	plan, err := s.buildRefundPlan(ctx, q, locked, amount, nil)
	if err != nil {
		return err
	}

	// THE REFUND ROW. This path moves money and posts a journal, and until now it
	// wrote no `refunds` row -- so the cumulative cap, which reads that table,
	// could not see it. A returned item and a gateway refund on the same order were
	// each individually capped against a total that excluded the other:
	//
	//	return one Rp50,000 item of a Rp100,000 order  -> no row, cap still reads 0
	//	gateway refund the remaining Rp100,000          -> cap reads 0, ALLOWS it
	//	                                               -> Rp150,000 out on Rp100,000
	//
	// That is not a race and needs no provider retry to happen; it is what a
	// returned item plus a later refund does on any ordinary order. `refunds` is
	// meant to be the authoritative record for EVERY path, and this was the one
	// path that moved money without writing to it.
	//
	// `succeeded`, and inside the caller's transaction: the money movement and the
	// journal that records it are in the same commit, so a refund that is counted
	// is a refund that happened. `pending` would be wrong here (nothing is in
	// flight -- the money has already moved) and so would `submitted` (there is no
	// provider to settle it).
	//
	// gateway_ref is empty because this path credits a wallet rather than calling
	// the provider, so there is no provider id for it. That divergence is
	// deliberate and separate; see SellerService.RefundReturn.
	refund := &repository.Refund{
		ID:              newRefundID(),
		PaymentIntentID: locked.ID,
		OrderID:         orderID,
		Gateway:         locked.Gateway,
		Amount:          plan.Amount,
		Reason:          reason,
		Status:          RefundStateSucceeded,
		RequestedAt:     time.Now().UTC(),
	}
	if err := s.payments.CreateRefund(ctx, q, refund); err != nil {
		return err
	}

	if err := s.payments.SetIntentStatusGuardedTx(ctx, q, locked.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded},
		plan.NextStatus); err != nil {
		return err
	}

	if plan.WasReleased {
		// Reverse only what the seller actually received, and reverse the
		// platform's commission so a refunded sale earns none of it. The legs
		// were derived in buildRefundPlan so they sum to `plan.Amount` exactly.
		if plan.RefundSeller > 0 {
			if err := s.payments.WalletTxOn(ctx, q, order.SellerID, "debit",
				domain.TxReasonRefund, plan.RefundSeller, orderID); err != nil {
				return err
			}
		}
		if plan.RefundFee > 0 {
			if err := s.payments.WalletTxOn(ctx, q, platformWalletID, "debit",
				domain.TxReasonRefund, plan.RefundFee, orderID); err != nil {
				return err
			}
		}
	}
	if refundToBuyer {
		if err := s.payments.WalletTxOn(ctx, q, order.BuyerID, "credit",
			domain.TxReasonRefund, plan.Amount, orderID); err != nil {
			return err
		}
	}
	s.postRefundJournal(ctx, q, order, locked, plan, refundToBuyer, reason)

	payStatus := domain.PaymentRefunded
	if plan.Partial {
		payStatus = domain.PaymentPartiallyRefunded
	}
	if err := tx.SetPaymentStatus(ctx, orderID, payStatus); err != nil {
		return err
	}
	if addEvent {
		note := "refund: " + reason
		if plan.Partial {
			note = fmt.Sprintf("partial refund: %s (Rp %.0f, Rp %.0f still refundable of Rp %.0f charged)",
				reason, plan.Amount, moneyRound(plan.Remaining-plan.Amount), locked.Amount)
		}
		if err := tx.AddEvent(ctx, &domain.OrderEvent{
			OrderID: orderID, FromStatus: order.Status, ToStatus: order.Status,
			ActorID: nil, Note: note,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emailOrder sends a templated email to the order's buyer.
//
// Best-effort by design: every caller is a POST-COMMIT path where the money has
// already moved, and returning an error would invite a retry of a refund. The
// order event is the durable record; this is a convenience.
func (s *PaymentService) emailOrder(ctx context.Context, o *domain.Order, templateName, subject string, data map[string]any) {
	if s.mailer == nil || s.users == nil || o == nil {
		return
	}
	buyer, err := s.users.ByID(ctx, o.BuyerID)
	if err != nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Name"] = buyer.FullName
	data["OrderNumber"] = o.OrderNumber
	data["OrderURL"] = s.webURL + "/orders/" + o.ID
	_ = s.mailer.Send(ctx, buyer.Email, subject, templateName, data)
}

// onRefunded processes a gateway-initiated refund of a specific amount.
//
// amount is the REFUNDED amount, which equals intent.Amount for a full refund
// and is smaller for a partial one. Passing the total unconditionally is what
// turned a Rp 10 partial refund into a Rp 100.000 credit.
// onRefunded records a refund the PROVIDER has already performed.
//
// It calls RecordProviderRefund and NOT RefundOrder. Those look interchangeable
// and are not:
//
//	provider refunds Rp30,000, notifies us
//	RefundOrder -> ExecuteRefund -> gw.Refund(Rp30,000)   <- a SECOND refund
//
// Midtrans caps cumulative refunds at the charge, so a second full refund is
// rejected and merely noisy. A partial one is not: 30,000 + 30,000 is still under
// a Rp100,000 charge, so the provider accepts it and the buyer has been paid
// Rp60,000. That bug was introduced when the gateway-refund path was unified, and
// this function is where it lived.
//
// The terminal-status guard below is a SECOND line of defence, not the primary
// one. It only catches replays of a notification about an order we already fully
// refunded; it cannot catch a replayed PARTIAL refund, which is the case that
// actually double-paid. That protection is the idempotency key inside
// RecordProviderRefund.
func (s *PaymentService) onRefunded(
	ctx context.Context, intent *domain.PaymentIntent, amount float64, ev *payments.GatewayEvent,
) error {
	// Already terminal: a replayed refund notification is a no-op, not an error.
	// Returning an error here makes the gateway retry forever.
	//
	// partially_refunded is deliberately NOT in this list: a second partial
	// refund is legitimate and must be applied. Idempotency comes from the
	// provider's own refund reference, not from refusing the call.
	if intent.Status == domain.IntentRefunded || intent.Status == domain.IntentDisputedSplit {
		return nil
	}
	if amount <= 0 {
		return domain.E(domain.KindInvalid, "REFUND_AMOUNT_REQUIRED",
			"refund amount must be positive")
	}
	if amount > intent.Amount+0.01 {
		return domain.E(domain.KindConflict, "REFUND_EXCEEDS_CHARGE",
			fmt.Sprintf("refund %.2f exceeds the charged amount %.2f", amount, intent.Amount))
	}

	// The provider's own refund id, taken from the notification, is what makes
	// this idempotent. Threaded through as an argument rather than stashed on the
	// service: two webhooks arriving concurrently would race on service state and
	// the loser would dedupe against the wrong refund's key.
	reference := ""
	if ev != nil {
		if ref, ok := ev.Raw["refund_id"].(string); ok {
			reference = ref
		}
	}

	_, err := s.RecordProviderRefund(ctx, providerRefundEvent{
		OrderID:   intent.OrderID,
		Amount:    amount,
		Gateway:   intent.Gateway,
		Reference: reference,
		Reason:    "gateway refund",
	})
	return err
}

// IntentByOrder exposes the escrow record for an order. Used by the order
// service to decide whether a cancellation would orphan captured funds.
func (s *PaymentService) IntentByOrder(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	return s.payments.IntentByOrder(ctx, orderID)
}

// DisputeSplitCredit settles a "split" dispute: the platform mediates by
// crediting the buyer a share out of the platform wallet.
//
// Three things this function must do, and previously did not:
//
//  1. Record a settlement, so a replay cannot pay twice. It used to only READ
//     intent.Status and write money, so calling it repeatedly debited the
//     platform wallet and credited the buyer every single time. The only
//     backstop was wallets.balance >= 0, so one order could drain the entire
//     commission pool accumulated from every other seller.
//
//  2. Bound the payout by what the platform actually earned on this order.
//     The old formula paid intent.Amount/2, which on a Rp 500.000 order with a
//     2% fee meant paying Rp 250.000 out of a commission that was only ever
//     going to be Rp 10.000 — a ~25x amplification of a single dispute.
//
//  3. Run the wallet movements and the settlement record in ONE transaction, so
//     a crash cannot leave money moved with no marker (or a marker with no
//     money).
func (s *PaymentService) DisputeSplitCredit(ctx context.Context, orderID, disputeID, adminID, note string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Status != domain.IntentCaptured && intent.Status != domain.IntentReleased {
		return domain.E(domain.KindConflict, "ESCROW_NOT_HELD", "no settled payment to split")
	}

	// The platform may only give back what it took. Prefer the fee actually
	// applied to this intent; fall back to the configured rate when the intent
	// predates the fee snapshot.
	maxCredit := intent.FeeAmount
	if maxCredit <= 0 {
		cfg, err := s.payments.ActiveFee(ctx)
		if err == nil {
			fee, _ := repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)
			maxCredit = fee
		}
	}
	if maxCredit <= 0 {
		return domain.E(domain.KindConflict, "NO_COMMISSION_TO_OFFSET",
			"platform earned no commission on this order, so there is nothing to credit from")
	}

	// Median-style split of the commission actually earned, rounded down to a
	// whole sen. Never more than the commission, never negative.
	credit := math.Floor(maxCredit/2*100) / 100
	if credit <= 0 {
		return nil
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Claim the dispute in the same transaction. RowsAffected == 0 means
	// someone else already resolved it, so we must not move money.
	claimed, err := s.disputes.ClaimForResolution(ctx, tx.PgTx(), disputeID, "split", note)
	if err != nil {
		return err
	}
	if !claimed {
		return domain.E(domain.KindConflict, "DISPUTE_ALREADY_RESOLVED",
			"dispute has already been resolved")
	}

	// Debit the platform wallet first: WalletTxOn enforces balance >= 0, so if
	// the platform cannot cover it the whole transaction (including the claim)
	// rolls back and the dispute stays open for a human to retry.
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), platformWalletID, "debit", domain.TxReasonRefund, credit, orderID); err != nil {
		return err
	}
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), intent.BuyerID, "credit", domain.TxReasonRefund, credit, orderID); err != nil {
		return err
	}

	// Record the settlement on the payment intent. This is what makes a
	// replay conflict on the next call even if the dispute row were somehow
	// re-opened.
	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased}, domain.IntentDisputedSplit); err != nil {
		return err
	}

	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID:    orderID,
		FromStatus: domain.OrderPaid,
		ToStatus:   domain.OrderPaid,
		ActorID:    &adminID,
		Note: fmt.Sprintf("dispute %s resolved as split: Rp %.0f credited to buyer "+
			"(capped at the platform commission of Rp %.0f earned on this order)",
			disputeID, credit, maxCredit),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Wallet returns the user's wallet.
func (s *PaymentService) Wallet(ctx context.Context, userID string) (*domain.Wallet, error) {
	return s.payments.Wallet(ctx, userID)
}

// WalletTransactions lists the user's ledger.
func (s *PaymentService) WalletTransactions(ctx context.Context, userID string, limit int) ([]*domain.WalletTransaction, error) {
	return s.payments.Transactions(ctx, userID, limit)
}

// RequestPayout creates a withdrawal request and debits the wallet.
// The debit and the payout row are written in ONE transaction so a failure
// can never leave money debited with no payout record. The instant
// "sent" simulation only runs in development (sandboxAutoSend).
func (s *PaymentService) RequestPayout(ctx context.Context, userID string, amount float64, bankName, bankAccount string) (*domain.Payout, error) {
	if amount <= 0 {
		return nil, domain.E(domain.KindInvalid, "BAD_AMOUNT", "amount must be positive")
	}
	if strings.TrimSpace(bankName) == "" || strings.TrimSpace(bankAccount) == "" {
		return nil, domain.E(domain.KindInvalid, "BANK_REQUIRED", "bank name and account number are required")
	}
	// KYC/store gate: withdrawals only for verified sellers with active
	// stores (wired via SetPayoutGuard at startup).
	if s.payoutGuard != nil {
		if err := s.payoutGuard(ctx, userID); err != nil {
			return nil, err
		}
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), userID, "debit", domain.TxReasonPayout, amount, ""); err != nil {
		return nil, err
	}
	payout := &domain.Payout{
		ID:          uuid.NewString(),
		WalletID:    userID,
		Amount:      amount,
		Status:      "pending",
		BankName:    bankName,
		BankAccount: bankAccount,
	}
	if err := s.payments.CreatePayoutTx(ctx, tx.PgTx(), payout); err != nil {
		return nil, err
	}
	// The reservation is the point of the step. Without it a seller can request a
	// payout, and then a return comes in against the same balance: the reversal
	// finds nothing to claw back, because the money was already marked as sent.
	// Real marketplaces hold the balance for a T+n lag with no open return or
	// dispute, and this row is what makes that hold visible and releasable rather
	// than implicit in a balance column.
	if err := s.payments.ReserveSellerPending(ctx, tx.PgTx(), userID, payout.ID, amount); err != nil {
		return nil, err
	}
	// Payout journal, inside the payout transaction.
	//
	// Debit the seller's personal account, credit seller_pending. The money has
	// left what the seller can spend and entered the pipeline, but it has NOT left
	// the platform: it is cash we owe the seller, scheduled to go out. Booking it
	// straight to bank_clearing would report the transfer as already made when no
	// transfer has been attempted.
	s.postLedger(ctx, tx.PgTx(), JournalSpec{
		IdempotencyKey: "payout:" + payout.ID,
		TxType:         TxTypePayout,
		RefType:        "payout",
		RefID:          payout.ID,
		Note:           "payout requested; balance moved to the payout pipeline",
		Entries: withMeta(payoutRequestedEntries(userID, amount), map[string]any{
			"payout_id": payout.ID, "seller_id": userID, "amount": amount,
		}),
	})
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Sandbox/dev only: simulate an instant successful transfer.
	if s.sandboxAutoSend {
		if err := s.postPayoutSettled(ctx, payout.ID, fmt.Sprintf("payout_%s", payout.ID[:8])); err != nil {
			return nil, err
		}
		payout.Status = "sent"
		now := time.Now().UTC()
		payout.ProcessedAt = &now
	}
	return payout, nil
}

// ProcessPayout finalizes a pending withdrawal: 'sent' records the real transfer
// reference; 'failed' rejects it and returns the amount to the seller's wallet.
//
// Both branches run in a transaction that the ledger joins, because both move
// money. Previously the status change was one statement on the pool and the
// wallet credit was a separate transaction inside the repository, so the two could
// not be posted together and the ledger could not be told about either.
func (s *PaymentService) ProcessPayout(ctx context.Context, payoutID, action, ref string) error {
	switch action {
	case "sent":
		if strings.TrimSpace(ref) == "" {
			return domain.E(domain.KindInvalid, "REF_REQUIRED", "transfer reference is required to mark a payout sent")
		}
		return s.postPayoutSettled(ctx, payoutID, strings.TrimSpace(ref))
	case "failed":
		return s.failPayout(ctx, payoutID)
	default:
		return domain.E(domain.KindInvalid, "BAD_ACTION", "action must be sent or failed")
	}
}

// postPayoutSettled records a payout as transferred and posts the journal that
// closes it out.
//
// Debit seller_pending, credit bank_clearing. The money leaves a liability we
// still owe and becomes cash in the platform's own bank account, which is the
// point at which it genuinely has left.
func (s *PaymentService) postPayoutSettled(ctx context.Context, payoutID, ref string) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	p, err := s.payments.PayoutForUpdate(ctx, q, payoutID)
	if err != nil {
		return err
	}
	if err := s.payments.MarkPayoutSentTx(ctx, q, payoutID, ref); err != nil {
		return err
	}
	s.postLedger(ctx, q, JournalSpec{
		IdempotencyKey: "payout-sent:" + payoutID,
		TxType:         TxTypePayout,
		RefType:        "payout",
		RefID:          payoutID,
		Note:           "payout transferred: " + ref,
		Entries: withMeta(payoutSettledEntries(p.WalletID, p.Amount), map[string]any{
			"payout_id": payoutID, "seller_id": p.WalletID, "transfer_ref": ref,
		}),
	})
	return tx.Commit(ctx)
}

// failPayout rejects a pending payout and returns the amount to the seller.
//
// The reversal is the mirror of postPayoutSettled: credit the seller's account
// again, debit seller_pending, and release the reservation. Without the
// reservation release the held amount stays claimed forever, and without the
// journal the money reappears in the wallet with no record of where it came from.
func (s *PaymentService) failPayout(ctx context.Context, payoutID string) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	p, err := s.payments.PayoutForUpdate(ctx, q, payoutID)
	if err != nil {
		return err
	}
	if err := s.payments.MarkPayoutFailedTx(ctx, q, payoutID); err != nil {
		return err
	}
	if err := s.payments.WalletTxOn(ctx, q, p.WalletID, "credit",
		domain.TxReasonAdjustment, p.Amount, payoutID); err != nil {
		return err
	}
	s.postLedger(ctx, q, JournalSpec{
		IdempotencyKey: "payout-failed:" + payoutID,
		TxType:         TxTypeReversal,
		RefType:        "payout",
		RefID:          payoutID,
		Note:           "payout failed; balance returned to the seller",
		Entries: withMeta(payoutFailedEntries(p.WalletID, p.Amount), map[string]any{
			"payout_id": payoutID, "seller_id": p.WalletID,
		}),
	})
	if err := s.payments.ReleaseSellerReservation(ctx, q, payoutID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Payouts lists the user's withdrawals.
// DefaultPayoutLagDays is how long a seller's money is held before it can be
// released, when nothing overrides it.
//
// Seven days, which is the Indonesian marketplace norm and roughly matches a
// consumer return window plus the carrier's own delivery window. It is a DEFAULT
// rather than a rule: a per-seller schedule overrides it, and this is only the
// floor for a seller who has not chosen one.
const DefaultPayoutLagDays = 7

// PayoutLagBounds are the shortest and longest lag a seller may configure.
//
// A lag of zero would release money the moment an order is completed, which is
// before any return could have been filed -- the hold would be decorative. A lag
// beyond a quarter is not a payment term, it is a balance the seller cannot
// withdraw, and the complaint it generates is indistinguishable from a platform
// that has lost the money. Refusing the value at the edge is better than accepting
// it and discovering it in an operator queue.
const (
	MinPayoutLagDays = 1
	MaxPayoutLagDays = 90
)

// ReleaseExpiredReservations releases seller holds that are no longer needed.
//
// This backs `worker.TaskReleasePayoutReservations`, which was a string constant
// with no handler and no schedule entry: the job had a name, a comment explaining
// the fraud control it represented, and no way to ever run.
//
// A release is the EXACT MIRROR of a hold -- journal, wallet, reservation -- and
// the mirror matters more here than anywhere else in the payout path. The first
// version of this stamped `released_at` and nothing else, which would have marked
// money as withdrawable while it was still in `held_balance` and the seller's
// `wallets.balance` had never been credited back. The hold would have become
// permanent in the one direction that matters.
//
// Each release is its own transaction, claiming the hold first. Claiming before
// moving the money is what makes a concurrent second run safe: its UPDATE matches
// nothing, it moves no money, and the hold is released exactly once.
//
// Returns the number released, so the worker's log line says whether the job is
// doing anything. A job that logs nothing is indistinguishable from a job that is
// broken.
func (s *PaymentService) ReleaseExpiredReservations(ctx context.Context, limit int) (int, error) {
	if s.ledger == nil {
		return 0, domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so a hold could be released with no mirror "+
				"journal; refusing rather than making money spendable that the books "+
				"still say is held")
	}

	candidates, err := s.payments.ReleasableReservations(ctx, s.payments.Pool(), s.PayoutLagDays(), limit)
	if err != nil {
		return 0, err
	}

	released := 0
	for _, res := range candidates {
		if err := s.releaseOneReservation(ctx, res); err != nil {
			// One hold failing must not stop the rest: a permanently-held balance for
			// a seller whose row has a data problem is worse than a delayed release
			// for someone else. Logged loudly, and counted as not released.
			slog.Error("could not release a seller hold; it stays held",
				"reservation_id", res.ID, "seller_id", res.SellerID,
				"order_id", res.OrderID, "hold_kind", res.Kind,
				"amount", res.Amount, "error", err.Error())
			continue
		}
		released++
	}
	return released, nil
}

// releaseOneReservation releases a single hold: claim, journal, move money, commit.
func (s *PaymentService) releaseOneReservation(ctx context.Context, res *repository.SellerReservation) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	// Claim FIRST. If another run already released this, the UPDATE matches nothing
	// and no money moves -- which is the only thing standing between a retried job
	// and crediting the seller twice.
	claimed, err := s.payments.MarkReservationReleasedTx(ctx, q, res.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	if err := s.ledger.EnsurePersonalAccount(ctx, q, res.SellerID); err != nil {
		return err
	}
	if err := s.ledger.EnsureHeldAccount(ctx, q, res.SellerID); err != nil {
		return err
	}

	if err := s.postLedgerStrict(ctx, q, JournalSpec{
		IdempotencyKey: "seller_hold_release:" + res.ID,
		TxType:         TxTypeSellerHold,
		RefType:        "order",
		RefID:          res.OrderID,
		Note:           "seller hold released: " + res.Kind,
		Entries: withMeta(sellerHoldReleaseEntries(res.SellerID, res.Amount),
			holdMeta(res.SellerID, res.OrderID, res.Kind, res.Amount)),
	}); err != nil {
		return err
	}

	// The wallet leg. HELD_BALANCE_UNDERFLOW here would mean the reservation and the
	// wallet disagree about how much is held, and it is deliberately fatal: a
	// release that cannot complete must leave the hold held, not free the
	// reservation and strand the balance.
	if err := s.payments.WalletHeldTxOn(ctx, q, res.SellerID, "release",
		repository.WalletReasonHoldRelease, res.Amount); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetPayoutLag sets the platform-wide payout lag. A per-seller override, when one
// exists, still wins; this is the default for everyone else.
func (s *PaymentService) SetPayoutLag(days int) error {
	if days < MinPayoutLagDays || days > MaxPayoutLagDays {
		return domain.E(domain.KindInvalid, "PAYOUT_LAG_OUT_OF_RANGE",
			fmt.Sprintf("a payout lag of %d days is outside the supported range of %d to %d",
				days, MinPayoutLagDays, MaxPayoutLagDays))
	}
	s.payoutLagDays = days
	return nil
}

// PayoutLagDays reports the platform default in force.
func (s *PaymentService) PayoutLagDays() int {
	if s.payoutLagDays <= 0 {
		return DefaultPayoutLagDays
	}
	return s.payoutLagDays
}

// Seller hold kinds. Each names a reversal that could still arrive, which is why
// the money is not withdrawable yet.
const (
	// HoldCOD is cash the courier is still holding.
	HoldCOD = "cod"
	// HoldReturn is money an open return claim could reverse.
	HoldReturn = "return"
	// HoldDispute is money an open dispute could reverse.
	HoldDispute = "dispute"
)

// HoldSellerFunds takes a hold against a seller's withdrawable balance.
//
// The fraud it prevents, in every case: the seller withdraws money that a return
// or dispute is about to reverse, and by the time the reversal runs there is
// nothing to take. `WalletTxOn` enforces balance >= 0, so the reversal does not go
// negative -- it fails, and the buyer's refund is stranded behind
// INSUFFICIENT_BALANCE with no path forward. The hold is what makes the balance
// honest, and "honest" is literal: the money leaves `wallets.balance`.
//
// ONE TRANSACTION, FOUR WRITES.
//
//	ensure the two personal ledger accounts exist
//	journal   debit seller_available, credit seller_held
//	wallets   balance -= amount, held_balance += amount
//	insert    the seller_reservations row
//
// If any of them fails, all four roll back. Splitting them is precisely how the two
// preceding commits got into trouble: the release job ran against holds nothing
// took, and then holds were taken that nothing consulted. Both halves existed and
// neither was connected to the balance.
//
// The journal is posted STRICTLY here, unlike `postLedger`. This is one event
// rather than a movement that has already committed, so a swallowed error would
// leave the balance moved with no accounting entry and nothing to notice it.
//
// `eventID` is the natural key of whatever the hold is FOR -- a return claim id, a
// dispute id, a payment intent id. It makes the journal idempotent across a retry,
// and it is why a hold can be taken again after a release without the second being
// swallowed as a duplicate of the first.
//
// CALL IT BEFORE THE EVENT IT PROTECTS AGAINST, not after.
//
//	hold first  -> if the return write fails, a stray hold exists
//	event first -> if the hold write fails, the seller can withdraw and the buyer
//	               is stuck
//
// A stray hold is the recoverable direction: it self-releases once the lag passes
// with no open return or dispute, which is exactly the condition that will be true
// if the return never happened. The other direction has no automatic repair.
//
// A live hold for this order and kind is NOT an error: the money is already
// protected, and refusing would reject a second claim for a hold it already has.
// The repository returns SELLER_HOLD_EXISTS for exactly that case and it is treated
// here as success.
func (s *PaymentService) HoldSellerFunds(
	ctx context.Context, sellerID, orderID, eventID, kind, note string, amount float64,
) error {
	if s.payments == nil {
		return domain.E(domain.KindConflict, "PAYMENTS_UNAVAILABLE",
			"the payment service is not wired, so a seller hold cannot be taken; "+
				"the balance is not protected against a return or dispute")
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.holdSellerFundsTx(ctx, tx.Querier(), sellerID, orderID, eventID, kind, note, amount); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// holdSellerFundsTx is the four writes against a caller-owned transaction.
//
// Split from HoldSellerFunds so a hold can join an event it is part of rather than
// racing it. The COD hold is the case that forced this: it belongs INSIDE
// `ReleaseEscrow`'s transaction, because that is the moment COD money first becomes
// withdrawable, and a separate transaction would leave a window where the seller
// could withdraw between the two.
//
// It returns nil for SELLER_HOLD_EXISTS for the same reason its caller does: the
// money is already protected.
func (s *PaymentService) holdSellerFundsTx(
	ctx context.Context, q repository.Querier,
	sellerID, orderID, eventID, kind, note string, amount float64,
) error {
	if s.ledger == nil {
		return domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so a seller hold would move money with no "+
				"accounting entry; refusing rather than creating an untracked balance")
	}
	reason, err := walletReasonForHold(kind)
	if err != nil {
		return err
	}

	if err := s.ledger.EnsurePersonalAccount(ctx, q, sellerID); err != nil {
		return err
	}
	if err := s.ledger.EnsureHeldAccount(ctx, q, sellerID); err != nil {
		return err
	}

	if err := s.postLedgerStrict(ctx, q, JournalSpec{
		IdempotencyKey: "seller_hold:" + eventID + ":" + kind,
		TxType:         TxTypeSellerHold,
		RefType:        "order",
		RefID:          orderID,
		Note:           note,
		Entries: withMeta(sellerHoldEntries(sellerID, amount),
			holdMeta(sellerID, orderID, kind, amount)),
	}); err != nil {
		return err
	}

	if err := s.payments.WalletHeldTxOn(ctx, q, sellerID, "hold", reason, amount); err != nil {
		return err
	}

	if err := s.payments.ReserveSellerPendingTx(ctx, q, sellerID, orderID, kind, note, amount); err != nil {
		if domain.Is(err, domain.KindConflict, "SELLER_HOLD_EXISTS") {
			return nil
		}
		return err
	}
	return nil
}

// holdMeta labels the journal lines, so a trial balance grouped by tx_type can say
// what a hold was for without a second query.
func holdMeta(sellerID, orderID, kind string, amount float64) map[string]any {
	return map[string]any{
		"seller_id": sellerID, "order_id": orderID,
		"hold_kind": kind, "amount": amount,
	}
}

// walletReasonForHold maps a hold kind to the reason written to
// `wallet_transactions`, where the seller will read it.
//
// A lookup that FAILS LOUD on an unknown kind rather than falling back. The
// reason is also a key in `uq_wallet_tx_business_event`, so a shared fallback
// would collapse a return hold and a dispute hold on one order onto one index key
// and surface as a raw 23505 in the middle of a return claim -- a database error
// where a caller mistake belongs.
func walletReasonForHold(kind string) (string, error) {
	switch kind {
	case HoldCOD:
		return repository.WalletReasonHoldCOD, nil
	case HoldReturn:
		return repository.WalletReasonHoldReturn, nil
	case HoldDispute:
		return repository.WalletReasonHoldDispute, nil
	default:
		return "", domain.E(domain.KindInternal, "RESERVATION_KIND_INVALID",
			"a seller hold must be one of cod, return or dispute; "+strconv.Quote(kind)+
				" is not one, and an unmapped hold would be recorded under a reason "+
				"that collides with every other hold on the same order")
	}
}

// HeldOutstanding is how much of its sellers' money the platform is currently
// holding back, and which sellers.
//
// The companion to EscrowOutstanding, and it answers a question escrow cannot:
// escrow is money for ORDERS, a hold is money a SELLER has earned and may not yet
// spend. They move independently, and an operator watching only escrow sees a
// number that does not change when a return hold is taken -- which is what a hold
// looks like when it is working.
//
// The rows are returned alongside the total on purpose. A total tells an operator
// something is held; the rows tell them whose money and why, which is the part
// that can be acted on. A total alone is a number to worry about.
func (s *PaymentService) HeldOutstanding(ctx context.Context, liveOnly bool, limit int) (float64, []*repository.SellerHoldRow, error) {
	if s.payments == nil {
		return 0, nil, domain.E(domain.KindConflict, "PAYMENTS_UNAVAILABLE",
			"the payment service is not wired, so held balances cannot be reported")
	}
	return s.payments.SellerHeldTotal(ctx, s.payments.Pool(), liveOnly, limit)
}

// Payouts lists a seller's withdrawal history.
func (s *PaymentService) Payouts(ctx context.Context, userID string) ([]*domain.Payout, error) {
	return s.payments.Payouts(ctx, userID)
}

// AdminPayouts lists withdrawal requests for the operations queue.
func (s *PaymentService) AdminPayouts(ctx context.Context, status string) ([]*repository.AdminPayout, error) {
	return s.payments.PayoutsByStatus(ctx, status, 100)
}

// IntentForOrder returns the intent for an order.
func (s *PaymentService) IntentForOrder(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	return s.payments.IntentByOrder(ctx, orderID)
}

// SignWebhook produces a valid signature for a payload (dev driver tooling).
func (s *PaymentService) SignWebhook(payload []byte) string {
	for _, g := range s.gateways {
		if sg, ok := g.(interface{ SignForDev([]byte) string }); ok {
			return sg.SignForDev(payload)
		}
	}
	return ""
}

// PayoutBatchView is the batch as the API exposes it.
//
// It exists so `internal/httpapi/handler` never imports `internal/repository`: the
// import-boundary check rejects that outright, and rightly -- seo.go and
// admin_ops.go both did it and were paid for it. The repository row is the storage
// shape; this is the wire shape, and the two are allowed to drift.
type PayoutBatchView struct {
	ID        string  `json:"id"`
	BatchRef  string  `json:"batch_ref,omitempty"`
	Status    string  `json:"status"`
	LagDays   int     `json:"lag_days"`
	CutoffAt  string  `json:"cutoff_at"`
	Total     float64 `json:"total"`
	ItemCount int     `json:"item_count"`
	CreatedAt string  `json:"created_at"`
}

func payoutBatchView(b *repository.PayoutBatch) PayoutBatchView {
	return PayoutBatchView{
		ID:        b.ID,
		BatchRef:  b.BatchRef,
		Status:    b.Status,
		LagDays:   b.LagDays,
		CutoffAt:  b.CutoffAt.UTC().Format(time.RFC3339),
		Total:     b.Total,
		ItemCount: b.ItemCount,
		CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// BuildPayoutBatch groups the withdrawals that are old enough into a batch.
//
// It GROUPS. It does not pay, and it must never write `payouts.status` -- see the
// section comment above the repository's batch methods for why claiming a row out
// of `pending` strands its `seller_pending` balance with no repair.
//
// Returns the batch and how many withdrawals it newly grouped. `created` is false
// when the receipt already existed, which is the normal result of a retried run and
// the reason the worker logs it rather than treating it as an error.
func (s *PaymentService) BuildPayoutBatch(
	ctx context.Context, lagDays int, cutoff time.Time, limit int,
) (PayoutBatchView, int, bool, error) {
	if lagDays < MinPayoutLagDays || lagDays > MaxPayoutLagDays {
		return PayoutBatchView{}, 0, false, domain.E(domain.KindInvalid, "PAYOUT_LAG_OUT_OF_RANGE",
			fmt.Sprintf("a batch lag of %d days is outside the supported range of %d to %d",
				lagDays, MinPayoutLagDays, MaxPayoutLagDays))
	}
	if cutoff.IsZero() {
		cutoff = time.Now().UTC()
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	// The receipt. Keyed on the lag AND the cutoff day, so a T+2 and a T+7 batch
	// for the same day are different batches rather than fighting over one ref, and
	// so a retried run finds its own.
	ref := fmt.Sprintf("payouts:%d:%s", lagDays, cutoff.UTC().Format("2006-01-02"))
	batchID, created, err := s.payments.CreatePayoutBatch(ctx, q, ref, cutoff, lagDays,
		fmt.Sprintf("T+%d payout batch", lagDays))
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}

	// A batch that already exists and is no longer a DRAFT must not be topped up.
	// Its contents were approved as a set, and silently adding another withdrawal to
	// an approved batch would change what was approved.
	batch, err := s.payments.PayoutBatchByID(ctx, q, batchID)
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	if batch.Status != repository.PayoutBatchDraft {
		return payoutBatchView(batch), batch.ItemCount, created, nil
	}

	// Only withdrawals older than the lag. `created_at` is the request time, which
	// is the moment the money left the seller's balance -- not `requested_at`, which
	// is the same column here but is NOT the same thing for an order placed before
	// a KYC check delayed the payout request.
	eligibleCutoff := cutoff.AddDate(0, 0, -lagDays)
	candidates, err := s.payments.UngroupedPayouts(ctx, q, eligibleCutoff, limit)
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	added, err := s.payments.AddPayoutBatchItems(ctx, q, batchID, candidates)
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	if err := s.payments.RecountPayoutBatch(ctx, q, batchID); err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PayoutBatchView{}, 0, false, err
	}

	batch, err = s.payments.PayoutBatchByID(ctx, s.payments.Pool(), batchID)
	if err != nil {
		return PayoutBatchView{}, 0, false, err
	}
	return payoutBatchView(batch), added, created, nil
}

// ApprovePayoutBatch records an operator's approval of a batch.
//
// It approves the GROUPING, not the money. Every withdrawal inside is still settled
// individually through the existing admin endpoint, because that is where the
// transfer reference and the failure handling live -- and because a batch that could
// settle would have to move money it does not know how to move.
func (s *PaymentService) ApprovePayoutBatch(ctx context.Context, batchID, approvedBy string) (int, error) {
	if approvedBy == "" {
		return 0, domain.E(domain.KindInvalid, "PAYOUT_BATCH_APPROVER_REQUIRED",
			"a batch approval records who approved it; without that the approval is "+
				"not an audit trail")
	}
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	items, status, err := s.payments.ApprovePayoutBatchTx(ctx, q, batchID, approvedBy)
	if err != nil {
		return 0, err
	}
	// An approval of an empty batch approves nothing and says so, rather than
	// recording an operator having reviewed a file with no rows in it.
	if items == 0 {
		return 0, domain.E(domain.KindConflict, "PAYOUT_BATCH_EMPTY",
			"this batch has no withdrawals in it, so there is nothing to approve; "+
				"it may be an operator running the job before any payout is due")
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	_ = status
	return items, nil
}

// PayoutBatches lists batches for the operator queue.
func (s *PaymentService) PayoutBatches(ctx context.Context, status string, limit int) ([]PayoutBatchView, error) {
	batches, err := s.payments.PayoutBatches(ctx, s.payments.Pool(), status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PayoutBatchView, 0, len(batches))
	for _, b := range batches {
		out = append(out, payoutBatchView(b))
	}
	return out, nil
}

// PayoutRemittanceCSV builds the file an operator hands the bank.
//
// REFUSES AN UNAPPROVED BATCH. The file IS the instruction to move money, and
// generating it from a draft means a batch run -- which happens daily, unattended --
// produces a payment instruction nobody looked at. The approval is the only thing
// standing between a scheduled job and an outbound transfer.
//
// And it does not settle anything. The file is an instruction a human executes; the
// actual state change is `ProcessPayout`, per payout, with a real transfer
// reference.
func (s *PaymentService) PayoutRemittanceCSV(ctx context.Context, batchID string) ([]byte, error) {
	batch, err := s.payments.PayoutBatchByID(ctx, s.payments.Pool(), batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != repository.PayoutBatchApproved &&
		batch.Status != repository.PayoutBatchSubmitted &&
		batch.Status != repository.PayoutBatchPaid {
		return nil, domain.E(domain.KindConflict, "PAYOUT_BATCH_NOT_APPROVED",
			"this batch is "+batch.Status+", so it has not been approved and must not "+
				"be turned into a payment instruction")
	}

	rows, err := s.payments.PayoutBatchRemittance(ctx, s.payments.Pool(), batchID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.E(domain.KindConflict, "PAYOUT_BATCH_EMPTY",
			"this batch has no withdrawals in it, so there is nothing to remit")
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	// The header names the amount as IDR and the file carries the batch ref, so a
	// bank that receives two files can tell them apart without asking.
	if err := w.Write([]string{
		"batch_ref", "payout_id", "seller_id", "bank_name", "bank_account",
		"amount_idr", "requested_at",
	}); err != nil {
		return nil, err
	}
	for _, p := range rows {
		// EVERY seller-controlled field goes through domain.CSVCell.
		//
		// `bank_name` and `bank_account` are whatever the seller typed into their store
		// profile. Unescaped, a seller could set bank_name to
		//
		//	=HYPERLINK("http://evil.example","approve this batch")
		//
		// and the formula would execute on the machine of whoever opened the file. That
		// reader is a finance operator or a bank, not another seller, which makes this
		// the highest-value injection point in the product: the target is a person
		// approving a payment instruction.
		//
		// The numeric amount and the RFC3339 timestamp are left alone -- neither can
		// begin with a formula character, and running them through the text escaper
		// would obscure a formatting bug rather than prevent one.
		row, err := domain.CSVRowMixed([]string{
			batch.BatchRef, p.ID, p.WalletID, p.BankName, p.BankAccount,
			strconv.FormatFloat(p.Amount, 'f', 0, 64),
			p.RequestedAt.UTC().Format(time.RFC3339),
		}, 0, 1, 2, 3, 4)
		if err != nil {
			return nil, err
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
