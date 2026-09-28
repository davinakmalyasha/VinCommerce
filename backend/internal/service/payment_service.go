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
	return &PaymentService{payments: payRepo, orders: orders, gateways: reg, defaultGW: primary, baseURL: baseURL}
}

// SetUsers enables buyer lookup for transactional emails.
func (s *PaymentService) SetUsers(u *repository.UserRepository) { s.users = u }

// SetDisputes lets a money-moving dispute decision claim the dispute row
// inside its own transaction, so the claim and the wallet movements commit
// or roll back together.
func (s *PaymentService) SetDisputes(d *repository.DisputeRepository) { s.disputes = d }

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
	// The order moves pending→paid with payment_status=pending (obligation
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
// only the money state (intent → captured, payment_status → paid) moves.
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
		return s.onRefunded(ctx, intent, intent.Amount)
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
		return s.onRefunded(ctx, intent, amount)
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

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// moneyRound rounds to 2 decimal places, half away from zero.
//
// The schema stores money as NUMERIC(14,2) but the Go layer is float64, so
// every value that reaches SQL is silently re-rounded by Postgres. Rounding
// in Go first makes the value the application logs, compares and stores all
// agree, instead of differing by a sen in a way nobody can see. A proper
// fix is a decimal type end to end; this is the interim that removes the
// worst of the drift.
func moneyRound(v float64) float64 {
	return math.Round(v*100) / 100
}

// refundedTotal sums the refund legs already written to the ledger for an
// order.
//
// The intent status cannot answer this: a `partially_refunded` intent does not
// record how much has already gone back, so two individually-valid partial
// refunds could together exceed the charge. The ledger is the only source
// that is guaranteed to agree with the money that actually moved.
func (s *PaymentService) refundedTotal(ctx context.Context, orderID string) (float64, error) {
	var total float64
	err := s.payments.SumRefundedByOrder(ctx, orderID, &total)
	if err != nil {
		return 0, err
	}
	return moneyRound(total), nil
}

// escrowWasReleased reports whether this order's escrow was ever paid out to
// the seller, read from the ledger rather than the intent status. A
// partially_refunded intent has lost that bit of information.
func (s *PaymentService) escrowWasReleased(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var released bool
	err := s.payments.HasEscrowRelease(ctx, tx, orderID, &released)
	if err != nil {
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
	// pending→paid ONLY. If the order moved on meanwhile (cancelled by the
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

	if intent.FeeAmount == 0 {
		cfg, err := s.payments.ActiveFeeTx(ctx, tx.PgTx())
		if err != nil {
			return err
		}
		fee, sellerAmount = repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)
		if err := s.payments.ApplyFeeTx(ctx, tx.PgTx(), intent.ID, fee, sellerAmount); err != nil {
			return err
		}
	}

	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
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
	// intent can never be refunded again — not for the remainder, not at all.
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

	if amount <= 0 {
		amount = intent.Amount
	}
	// The cumulative refunded total is derived from the wallet ledger rather
	// than a column, because that is the only thing guaranteed to be
	// consistent with the money that actually moved. Without it, two
	// successive partial refunds could each be individually valid and together
	// refund more than was charged.
	alreadyRefunded, err := s.refundedTotal(ctx, orderID)
	if err != nil {
		return err
	}
	remaining := moneyRound(intent.Amount - alreadyRefunded)
	if remaining <= 0 {
		return domain.E(domain.KindConflict, "ALREADY_REFUNDED",
			"this order has already been fully refunded")
	}
	if amount > remaining+0.01 {
		return domain.E(domain.KindConflict, "REFUND_EXCEEDS_REMAINING",
			fmt.Sprintf("refund %.2f exceeds the remaining refundable %.2f of %.2f charged",
				amount, remaining, intent.Amount))
	}
	partial := absDiff(amount, remaining) > 0.01

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Whether the escrow was ever released. Derived from the ledger rather than
	// the intent status, because a partially_refunded intent has lost that
	// bit: it could have been released before the first partial refund, or
	// still held in escrow.
	wasReleased, err := s.escrowWasReleased(ctx, tx.PgTx(), orderID)
	if err != nil {
		return err
	}

	// The fee actually applied to this intent, so the reversal is exact.
	// Falls back to the configured rate for intents that predate the snapshot.
	fee := intent.FeeAmount
	sellerAmount := intent.SellerAmount
	if wasReleased && fee == 0 {
		if cfg, ferr := s.payments.ActiveFeeTx(ctx, tx.PgTx()); ferr == nil {
			fee, sellerAmount = repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)
		}
	}
	if fee < 0 {
		fee = 0
	}
	if sellerAmount < 0 {
		sellerAmount = 0
	}

	// Scale both legs to THIS refund's share of the charge, then make the
	// seller's leg the residual so the debit and the credit sum to `amount` by
	// construction. Independently rounding two scaled values drifted by a sen
	// per partial refund (100.00 charge, 2.50 fee, 33.33 refunded -> 32.49 +
	// 0.83 debited vs 33.33 credited), and the drift is permanent because it
	// accumulates in the wallet balances.
	refundFee := 0.0
	refundSeller := 0.0
	if wasReleased && intent.Amount > 0 {
		ratio := amount / intent.Amount
		refundFee = moneyRound(fee * ratio)
		refundSeller = moneyRound(amount - refundFee)
		if refundSeller < 0 {
			refundSeller = 0
		}
	}

	next := domain.IntentPartiallyRefunded
	if !partial {
		next = domain.IntentRefunded
	}

	// Guarded in SQL: a concurrent second refund loses here and never reaches
	// the ledger writes below, so two simultaneous refunds cannot both pay out.
	// The cumulative total is re-derived inside the caller, but the row lock
	// this UPDATE takes is what actually serialises them.
	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded}, next); err != nil {
		return err
	}

	if wasReleased {
		// Debit only what the seller actually received, and reverse the
		// platform's commission so a refunded sale earns no commission. The
		// two legs are derived so they sum to exactly `amount`.
		if refundSeller > 0 {
			if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.SellerID, "debit", domain.TxReasonRefund, refundSeller, orderID); err != nil {
				return err
			}
		}
		if refundFee > 0 {
			if err := s.payments.WalletTxOn(ctx, tx.PgTx(), platformWalletID, "debit", domain.TxReasonRefund, refundFee, orderID); err != nil {
				return err
			}
		}
	}
	if refundToBuyer {
		if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.BuyerID, "credit", domain.TxReasonRefund, amount, orderID); err != nil {
			return err
		}
	}

	payStatus := domain.PaymentRefunded
	if partial {
		payStatus = domain.PaymentPartiallyRefunded
	}
	if err := tx.SetPaymentStatus(ctx, orderID, payStatus); err != nil {
		return err
	}
	note := "refund: " + reason
	if partial {
		note = fmt.Sprintf("partial refund: %s (Rp %.2f, Rp %.2f still refundable of Rp %.2f charged)",
			reason, amount, moneyRound(remaining-amount), intent.Amount)
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: order.Status, ToStatus: order.Status,
		ActorID: nil, Note: note,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// onRefunded processes a gateway-initiated refund of a specific amount.
//
// amount is the REFUNDED amount, which equals intent.Amount for a full refund
// and is smaller for a partial one. Passing the total unconditionally is what
// turned a Rp 10 partial refund into a Rp 100.000 credit.
func (s *PaymentService) onRefunded(ctx context.Context, intent *domain.PaymentIntent, amount float64) error {
	// Already terminal: a replayed refund notification is a no-op, not an
	// error. Returning an error here makes the gateway retry forever.
	//
	// partially_refunded is deliberately NOT in this list: a second partial
	// refund is legitimate and must be applied. Idempotency comes from the
	// cumulative-total check in RefundOrder plus the guarded intent UPDATE, not
	// from refusing the call.
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
	refundToBuyer := true
	return s.RefundOrder(ctx, intent.OrderID, "gateway refund", refundToBuyer, amount)
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
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Sandbox/dev only: simulate an instant successful transfer.
	if s.sandboxAutoSend {
		if err := s.payments.MarkPayoutSent(ctx, payout.ID, fmt.Sprintf("payout_%s", payout.ID[:8])); err != nil {
			return nil, err
		}
		payout.Status = "sent"
		now := time.Now().UTC()
		payout.ProcessedAt = &now
	}
	return payout, nil
}

// Payouts lists the user's withdrawals.
func (s *PaymentService) Payouts(ctx context.Context, userID string) ([]*domain.Payout, error) {
	return s.payments.Payouts(ctx, userID)
}

// AdminPayouts lists withdrawal requests for the operations queue.
func (s *PaymentService) AdminPayouts(ctx context.Context, status string) ([]*repository.AdminPayout, error) {
	return s.payments.PayoutsByStatus(ctx, status, 100)
}

// ProcessPayout finalizes a pending withdrawal: 'sent' records the real
// transfer reference; 'failed' rejects it and refunds the seller's wallet.
func (s *PaymentService) ProcessPayout(ctx context.Context, payoutID, action, ref string) error {
	switch action {
	case "sent":
		if strings.TrimSpace(ref) == "" {
			return domain.E(domain.KindInvalid, "REF_REQUIRED", "transfer reference is required to mark a payout sent")
		}
		return s.payments.CompletePayout(ctx, payoutID, strings.TrimSpace(ref))
	case "failed":
		return s.payments.FailPayout(ctx, payoutID)
	default:
		return domain.E(domain.KindInvalid, "BAD_ACTION", "action must be sent or failed")
	}
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
