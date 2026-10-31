package service

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/payments"
)

// Wallet top-up.
//
// The wallet had no way in. Every credit came from a refund or a dispute
// split, so "pay with wallet" was unreachable for a normal buyer and a refund
// could only ever be spent, not replenished. This file adds the missing
// direction: cash from the gateway becomes buyer balance, recorded in the same
// double-entry ledger as every other movement.

// CreateTopUpInput describes a buyer's deposit.
type CreateTopUpInput struct {
	UserID string
	Email  string
	Amount float64
}

// CreateTopUp opens a gateway charge for a wallet deposit.
//
// The database row is committed BEFORE the provider is called. A charge the
// provider accepted but this system never recorded is money a buyer paid for
// nothing; a row with no charge is inert and expires. The same ordering the
// refund path uses, for the same reason.
func (s *PaymentService) CreateTopUp(ctx context.Context, in CreateTopUpInput) (*domain.WalletTopUp, error) {
	amount := moneyRound(in.Amount)
	if math.Abs(in.Amount-amount) > 0.0001 || !IsWholeIDR(amount) {
		return nil, domain.E(domain.KindInvalid, "BAD_AMOUNT",
			"top-up amount must be whole rupiah")
	}
	if amount < domain.MinTopUpAmount || amount > domain.MaxTopUpAmount {
		return nil, domain.E(domain.KindInvalid, "BAD_AMOUNT",
			fmt.Sprintf("top-up must be between Rp %.0f and Rp %.0f",
				float64(domain.MinTopUpAmount), float64(domain.MaxTopUpAmount)))
	}

	gw, ok := s.gateways[s.defaultGW]
	if !ok {
		gw, ok = s.gateways["sandbox"]
	}
	if !ok {
		return nil, domain.E(domain.KindInternal, "NO_GATEWAY", "no payment gateway configured")
	}

	id := uuid.NewString()
	topUp := &domain.WalletTopUp{
		ID:      id,
		UserID:  in.UserID,
		Amount:  amount,
		Status:  domain.TopUpPending,
		Gateway: gw.Name(),
		// Placeholder: the column is NOT NULL UNIQUE so the row can commit
		// before the provider has issued its own reference.
		GatewayRef: "pending-" + id,
	}
	if err := s.payments.CreateTopUp(ctx, topUp); err != nil {
		return nil, err
	}

	finishBase := s.webURL
	if finishBase == "" {
		finishBase = s.baseURL
	}
	// "TU-" + a uuid is 39 characters, inside Midtrans's 50-character order_id
	// limit, and is recognisable in the provider dashboard as not-an-order.
	gwPayment, err := gw.CreatePayment(ctx, payments.CreatePaymentInput{
		OrderID:     id,
		OrderNumber: "TU-" + strings.ReplaceAll(id, "-", ""),
		Amount:      amount,
		Currency:    "IDR",
		BuyerEmail:  in.Email,
		Idempotency: "topup-" + id,
		ReturnURL:   finishBase + "/wallet",
	})
	if err != nil {
		// The buyer was not charged (the provider call failed), so the row is
		// marked failed rather than left pending forever.
		_ = s.payments.SetTopUpStatus(ctx, id, domain.TopUpFailed)
		return nil, domain.Wrap(domain.KindInternal, "GATEWAY_ERROR", "payment gateway unavailable", err)
	}
	if err := s.payments.SetTopUpGateway(ctx, id, gwPayment.Reference, gwPayment.Token, gwPayment.RedirectURL); err != nil {
		return nil, err
	}
	topUp.GatewayRef = gwPayment.Reference
	topUp.SnapToken = gwPayment.Token
	topUp.RedirectURL = gwPayment.RedirectURL
	return topUp, nil
}

// MyTopUps lists a buyer's deposits.
func (s *PaymentService) MyTopUps(ctx context.Context, userID string) ([]*domain.WalletTopUp, error) {
	return s.payments.TopUpsByUser(ctx, userID, 20)
}

// handleTopUpWebhook handles a notification whose reference is not an order
// intent. Called from HandleWebhook after that lookup returns NOT_FOUND.
func (s *PaymentService) handleTopUpWebhook(
	ctx context.Context, gatewayName string, ev *payments.GatewayEvent,
) error {
	topUp, err := s.payments.TopUpByGatewayRef(ctx, gatewayName, ev.Reference)
	if err != nil {
		// Neither an order nor a top-up: the original NOT_FOUND is the honest
		// answer, and the handler turns it into a 404 without moving money.
		return err
	}

	switch ev.Type {
	case payments.EventPaid:
		if ev.Amount <= 0 {
			return domain.E(domain.KindInvalid, "AMOUNT_REQUIRED",
				"webhook did not include a payable amount")
		}
		if absDiff(ev.Amount, topUp.Amount) > 0.01 {
			return domain.E(domain.KindConflict, "AMOUNT_MISMATCH",
				fmt.Sprintf("top-up webhook amount %.2f does not match %.2f",
					ev.Amount, topUp.Amount))
		}
		return s.captureTopUp(ctx, topUp)
	case payments.EventFailed:
		// A late failure must not undo a credit: only a still-pending row moves,
		// and the UPDATE decides that in the database rather than in a read-then-
		// write race with the paid webhook.
		if err := s.payments.FailTopUpIfPending(ctx, topUp.ID); err != nil {
			return err
		}
		return nil
	case payments.EventRefunded, payments.EventPartiallyRefunded:
		// Money can only be spent, not withdrawn, before this feature, so a
		// provider refund of a top-up has no supported path. Surface it instead
		// of silently ignoring the notification.
		return domain.E(domain.KindConflict, "TOPUP_REFUND_UNSUPPORTED",
			"a wallet top-up refund needs manual handling")
	}
	return nil
}

// captureTopUp credits the wallet exactly once.
//
// The wallet credit and its journal are ONE transaction, and the status flip is
// guarded, so a duplicate webhook (the provider retries until it gets a 2xx)
// cannot credit twice. If the money moves, the journal moves with it.
func (s *PaymentService) captureTopUp(ctx context.Context, topUp *domain.WalletTopUp) error {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	transitioned, err := s.payments.MarkTopUpPaidTx(ctx, tx.PgTx(), topUp.ID, topUp.Amount)
	if err != nil {
		return err
	}
	if !transitioned {
		// Already paid (a retry) or no longer pending (expired/failed). No money
		// moves; the provider gets a 200 so it stops retrying.
		return nil
	}

	if err := s.payments.WalletTxOn(ctx, tx.PgTx(), topUp.UserID, "credit",
		domain.TxReasonTopUp, topUp.Amount, topUp.ID); err != nil {
		return err
	}

	if err := s.postLedgerStrict(ctx, tx.PgTx(), JournalSpec{
		IdempotencyKey: "topup:" + topUp.ID,
		TxType:         TxTypeTopUp,
		RefType:        "wallet_topup",
		RefID:          topUp.ID,
		Note:           "wallet top-up",
		Entries: withMeta(topUpEntries(topUp.UserID, topUp.Amount), map[string]any{
			"wallet_topup_id": topUp.ID,
			"gateway":         topUp.Gateway,
			"amount":          topUp.Amount,
		}),
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ApproveSandboxTopUp completes a top-up against the fake provider by emitting
// the same signed webhook a real gateway would send. Development only -- the
// route is registered inside the non-production block, like the order driver.
//
// It deliberately goes through HandleWebhook rather than crediting directly:
// the dev path must exercise the production path, or the thing being tested is
// not the thing that ships.
func (s *PaymentService) ApproveSandboxTopUp(ctx context.Context, topUpID string) error {
	topUp, err := s.payments.TopUpByID(ctx, topUpID)
	if err != nil {
		return err
	}
	if topUp.Gateway != "sandbox" {
		return domain.E(domain.KindConflict, "NOT_SANDBOX",
			"only a sandbox top-up can be simulated")
	}
	if topUp.Status != domain.TopUpPending {
		return domain.E(domain.KindConflict, "TOPUP_STATE", "top-up is not awaiting payment")
	}
	payload := []byte(fmt.Sprintf(
		`{"type":%q,"reference":%q,"amount":%v,"currency":"IDR"}`,
		payments.EventPaid, topUp.GatewayRef, topUp.Amount))
	return s.HandleWebhook(ctx, "sandbox", payload, s.SignWebhook(payload))
}

// TopUp exposes one top-up to the handler (ownership is checked there).
func (s *PaymentService) TopUp(ctx context.Context, id string) (*domain.WalletTopUp, error) {
	return s.payments.TopUpByID(ctx, id)
}
