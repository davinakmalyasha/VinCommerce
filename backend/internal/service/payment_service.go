package service

import (
	"context"
	"errors"
	"fmt"
	"math"
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
		s.emailFor(ctx, order.ID, "order_paid", "Pembayaran diterima â€” VinCommerce",
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
			ActorID: &in.BuyerID, Note: "COD â€” bayar tunai saat barang diterima",
		}); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		s.publish(ctx, order.ID, domain.OrderPending, domain.OrderPaid, "COD â€” menunggu pelunasan saat diterima")
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
// COD capture must NOT touch order status â€” the order is already delivered;
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
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publish(ctx, intent.OrderID, "", "", "pembayaran COD tercatat")
	s.emailFor(ctx, intent.OrderID, "order_paid", "Pembayaran diterima â€” VinCommerce",
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
		// the intent â€” a webhook without an amount must not move money.
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
	// capture rolls back â€” a late webhook can never resurrect it.
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
	s.emailFor(ctx, intent.OrderID, "order_paid", "Pembayaran diterima â€” VinCommerce",
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
// wallet credits happen in ONE transaction â€” a crash can never release escrow
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

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.publish(ctx, orderID, order.Status, order.Status, "escrow released to seller")
	s.emailFor(ctx, orderID, "order_completed", "Pesanan selesai â€” dana escrow dilepas",
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
//     refund it (-seller, +buyer) â€” the seller's net was zero but their wallet
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
//     going to be Rp 10.000 â€” a ~25x amplification of a single dispute.
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
// the fraud control it represented, and no way to ever run. A hold that is never
// released is not a hold, it is a permanent deduction from a seller's balance --
// and a seller whose balance never becomes withdrawable stops selling, or
// complains, and both look like a payments bug.
//
// The release conditions themselves live in the query, because they are a
// conjunction over four tables and expressing them in Go would mean reading them
// into memory and deciding there, which is the shape that produces a hold released
// on a stale read. See PaymentRepository.ReleaseExpiredReservations.
//
// Returns the number released, so the worker's log line says whether the job is
// doing anything. A job that logs nothing is indistinguishable from a job that is
// broken.
func (s *PaymentService) ReleaseExpiredReservations(ctx context.Context, limit int) (int, error) {
	return s.payments.ReleaseExpiredReservations(
		ctx, s.payments.Pool(), s.PayoutLagDays(), limit)
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
