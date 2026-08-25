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
	case "payment.paid":
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
	case "payment.refunded":
		return s.onRefunded(ctx, intent)
	case "payment.failed":
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

// RefundOrder refunds an order fully (escrow -> buyer wallet, or seller wallet -> buyer).
// The intent transition is guarded so a refund can only ever execute once.
func (s *PaymentService) RefundOrder(ctx context.Context, orderID string, reason string, refundToBuyer bool) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Status != domain.IntentCaptured && intent.Status != domain.IntentReleased {
		return domain.E(domain.KindConflict, "NOT_REFUNDABLE",
			"only captured or released payments can be refunded")
	}
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}

	amount := intent.Amount
	wasReleased := intent.Status == domain.IntentReleased

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Guarded in SQL: the first refund wins; replays conflict here and never
	// reach the ledger writes below (no infinite money minting).
	if err := s.payments.SetIntentStatusGuardedTx(ctx, tx.PgTx(), intent.ID,
		[]string{domain.IntentCaptured, domain.IntentReleased}, domain.IntentRefunded); err != nil {
		return err
	}

	if wasReleased {
		// funds already with seller: debit seller wallet, credit buyer wallet
		if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.SellerID, "debit", domain.TxReasonRefund, amount, orderID); err != nil {
			return err
		}
	}
	if refundToBuyer {
		if err := s.payments.WalletTxOn(ctx, tx.PgTx(), order.BuyerID, "credit", domain.TxReasonRefund, amount, orderID); err != nil {
			return err
		}
	}

	if err := tx.SetPaymentStatus(ctx, orderID, domain.PaymentRefunded); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: order.Status, ToStatus: order.Status,
		ActorID: nil, Note: "refund: " + reason,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// onRefunded processes gateway-initiated refunds.
func (s *PaymentService) onRefunded(ctx context.Context, intent *domain.PaymentIntent) error {
	if intent.Status == domain.IntentRefunded || intent.Status == domain.IntentPartiallyRefunded {
		return nil
	}
	return s.RefundOrder(ctx, intent.OrderID, "gateway refund", true)
}

// DisputeSplitCredit settles a "split" dispute: the platform mediates by
// crediting the buyer HALF the paid amount out of the platform wallet.
// Escrow keeps its normal lifecycle (the seller's side resolves at release).
func (s *PaymentService) DisputeSplitCredit(ctx context.Context, orderID string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Status != domain.IntentCaptured && intent.Status != domain.IntentReleased {
		return domain.E(domain.KindConflict, "ESCROW_NOT_HELD", "no settled payment to split")
	}
	half := math.Floor(intent.Amount/2*100) / 100
	if half <= 0 {
		return nil
	}
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Platform absorbs its mediation share — debited from the platform wallet
	// so the ledger stays balanced (no money minted).
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), platformWalletID, "debit", domain.TxReasonRefund, half, orderID); err != nil {
		return err
	}
	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), intent.BuyerID, "credit", domain.TxReasonRefund, half, orderID); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: "", ToStatus: domain.OrderPaid,
		ActorID: nil, Note: fmt.Sprintf("dispute split settlement: Rp %.0f credited to buyer", half),
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
