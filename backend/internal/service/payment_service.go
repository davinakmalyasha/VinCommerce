package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	payments      *repository.PaymentRepository
	orders        *repository.OrderRepository
	users         *repository.UserRepository
	gateways      map[string]payments.Gateway // adapters by name ("sandbox", "midtrans", ...)
	defaultGW     string                      // gateway used for generic bank_transfer/e_wallet methods
	baseURL       string
	broker        *stream.Broker
	notifications *NotificationService
	mailer        *mail.Client
	webURL        string
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
	if in.Method == "wallet" {
		if err := s.payments.WalletTx(ctx, order.BuyerID, "debit", "order_payment", order.TotalAmount, order.ID); err != nil {
			return nil, nil, err
		}
		intent := &domain.PaymentIntent{
			ID: uuid.NewString(), OrderID: order.ID, BuyerID: in.BuyerID,
			Amount: order.TotalAmount, Currency: order.Currency, Status: domain.IntentInitiated,
			Gateway: "internal", Method: "wallet", IdempotencyKey: key,
		}
		if err := s.payments.CreateIntent(ctx, intent); err != nil {
			return nil, nil, err
		}
		if err := s.onPaid(ctx, intent); err != nil {
			return nil, nil, err
		}
		return intent, &payments.GatewayPayment{Reference: "wallet", Status: "paid"}, nil
	}

	// COD: no charge at checkout; captured on delivery confirmation.
	if in.Method == "cod" {
		intent := &domain.PaymentIntent{
			ID: uuid.NewString(), OrderID: order.ID, BuyerID: in.BuyerID,
			Amount: order.TotalAmount, Currency: order.Currency, Status: domain.IntentInitiated,
			Gateway: "cod", Method: "cod", IdempotencyKey: key,
		}
		if err := s.payments.CreateIntent(ctx, intent); err != nil {
			return nil, nil, err
		}
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
func (s *PaymentService) CaptureCOD(ctx context.Context, orderID string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Method != "cod" || intent.Status != domain.IntentInitiated {
		return nil
	}
	intent.Status = domain.IntentInitiated
	return s.onPaid(ctx, intent)
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

	switch ev.Type {
	case "payment.paid":
		// Never capture when the provider amount disagrees with the order.
		if ev.Amount > 0 && absDiff(ev.Amount, intent.Amount) > 0.01 {
			return domain.E(domain.KindConflict, "AMOUNT_MISMATCH",
				fmt.Sprintf("webhook amount %.2f does not match intent amount %.2f", ev.Amount, intent.Amount))
		}
		return s.onPaid(ctx, intent)
	case "payment.refunded":
		return s.onRefunded(ctx, intent)
	case "payment.failed":
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

	if err := s.payments.SetIntentStatus(ctx, intent.ID, domain.IntentCaptured); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := s.orders.SetPaymentStatus(ctx, intent.OrderID, domain.PaymentPaid); err != nil {
		return err
	}
	if err := s.orders.SetStatus(ctx, intent.OrderID, domain.OrderPaid); err != nil {
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
// deducting the platform commission.
func (s *PaymentService) ReleaseEscrow(ctx context.Context, orderID string) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if intent.Status != domain.IntentCaptured && intent.Status != domain.IntentReleased {
		return domain.E(domain.KindConflict, "ESCROW_NOT_HELD", "escrow is not held for this order")
	}
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}

	fee, sellerAmount := intent.Amount, 0.0
	if intent.FeeAmount == 0 {
		cfg, err := s.payments.ActiveFee(ctx)
		if err != nil {
			return err
		}
		fee, sellerAmount = repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)
		if err := s.payments.ApplyFee(ctx, intent.ID, fee, sellerAmount); err != nil {
			return err
		}
	} else {
		fee, sellerAmount = intent.FeeAmount, intent.SellerAmount
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.payments.SetIntentStatus(ctx, intent.ID, domain.IntentReleased); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: order.Status, ToStatus: order.Status,
		ActorID: nil, Note: fmt.Sprintf("escrow released to seller (komisi platform Rp %.0f)", fee),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// seller credited the net amount; platform fee recorded against the platform wallet.
	if err := s.payments.WalletTx(ctx, order.SellerID, "credit", domain.TxReasonEscrowRelease, sellerAmount, orderID); err != nil {
		return err
	}
	if err := s.payments.WalletTx(ctx, platformWalletID, "credit", "commission", fee, orderID); err != nil {
		return err
	}

	s.publish(ctx, orderID, order.Status, order.Status, "escrow released to seller")
	s.emailFor(ctx, orderID, "order_completed", "Pesanan selesai — dana escrow dilepas",
		map[string]any{"Total": fmt.Sprintf("Rp %.0f", sellerAmount)})
	return nil
}

// RefundOrder refunds an order fully (escrow -> buyer wallet, or seller wallet -> buyer).
func (s *PaymentService) RefundOrder(ctx context.Context, orderID string, reason string, refundToBuyer bool) error {
	intent, err := s.payments.IntentByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}

	amount := intent.Amount
	if err := s.payments.SetIntentStatus(ctx, intent.ID, domain.IntentRefunded); err != nil {
		return err
	}

	if intent.Status == domain.IntentReleased {
		// funds already with seller: debit seller wallet, credit buyer wallet
		if err := s.payments.WalletTx(ctx, order.SellerID, "debit", domain.TxReasonRefund, amount, orderID); err != nil {
			return err
		}
	}
	if refundToBuyer {
		if err := s.payments.WalletTx(ctx, order.BuyerID, "credit", domain.TxReasonRefund, amount, orderID); err != nil {
			return err
		}
	}

	if err := s.orders.SetPaymentStatus(ctx, orderID, domain.PaymentRefunded); err != nil {
		return err
	}
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
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

// Wallet returns the user's wallet.
func (s *PaymentService) Wallet(ctx context.Context, userID string) (*domain.Wallet, error) {
	return s.payments.Wallet(ctx, userID)
}

// WalletTransactions lists the user's ledger.
func (s *PaymentService) WalletTransactions(ctx context.Context, userID string, limit int) ([]*domain.WalletTransaction, error) {
	return s.payments.Transactions(ctx, userID, limit)
}

// RequestPayout creates a withdrawal request and debits the wallet.
func (s *PaymentService) RequestPayout(ctx context.Context, userID string, amount float64, bankName, bankAccount string) (*domain.Payout, error) {
	if amount <= 0 {
		return nil, domain.E(domain.KindInvalid, "BAD_AMOUNT", "amount must be positive")
	}
	if err := s.payments.WalletTx(ctx, userID, "debit", domain.TxReasonPayout, amount, ""); err != nil {
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
	if err := s.payments.CreatePayout(ctx, payout); err != nil {
		return nil, err
	}
	// Sandbox: simulate instant transfer.
	if err := s.payments.MarkPayoutSent(ctx, payout.ID, fmt.Sprintf("payout_%s", payout.ID[:8])); err != nil {
		return nil, err
	}
	payout.Status = "sent"
	now := time.Now().UTC()
	payout.ProcessedAt = &now
	return payout, nil
}

// Payouts lists the user's withdrawals.
func (s *PaymentService) Payouts(ctx context.Context, userID string) ([]*domain.Payout, error) {
	return s.payments.Payouts(ctx, userID)
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
