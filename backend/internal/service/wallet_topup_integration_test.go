package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
)

// The wallet had no way in: every credit came from a refund, so "pay with
// wallet" was unreachable for an ordinary buyer. This exercises the deposit
// end to end against Postgres -- create, webhook, credit -- and, more
// importantly, the retry the provider WILL send: a second delivery of the same
// paid event must not credit twice.
func TestWalletTopUpCreditsOnce(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	payRepo := repository.NewPaymentRepository(pool)
	svc := NewPaymentService(
		payRepo,
		repository.NewOrderRepository(pool),
		[]payments.Gateway{&payments.SandboxGateway{}},
		"sandbox",
		"http://localhost:8080",
	)
	svc.SetLedger(NewLedgerService(repository.NewLedgerRepository(pool), nil))

	var buyerID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = 'buyer.sample@vincommerce.com'`).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}

	topUp, err := svc.CreateTopUp(ctx, CreateTopUpInput{
		UserID: buyerID, Email: "buyer.sample@vincommerce.com", Amount: 50_000,
	})
	if err != nil {
		t.Fatalf("CreateTopUp: %v", err)
	}
	if topUp.Status != "pending" || topUp.GatewayRef == "" || topUp.GatewayRef == "pending-"+topUp.ID {
		t.Fatalf("top-up was not registered with the gateway: %+v", topUp)
	}

	balanceOf := func() float64 {
		t.Helper()
		var b float64
		if err := pool.QueryRow(ctx, `SELECT balance FROM wallets WHERE user_id = $1`, buyerID).Scan(&b); err != nil {
			t.Fatalf("read wallet: %v", err)
		}
		return b
	}
	// `wallets` rows are created lazily by the first movement, and the seeded
	// buyer has never moved money. Reading before creating is a test bug, not a
	// product one -- the first CI run failed here with "no rows in result set".
	if _, err := payRepo.Wallet(ctx, buyerID); err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	before := balanceOf()

	payload := []byte(fmt.Sprintf(
		`{"type":%q,"reference":%q,"amount":50000,"currency":"IDR"}`,
		payments.EventPaid, topUp.GatewayRef))
	gw := &payments.SandboxGateway{}
	sig := gw.SignForDev(payload)

	if err := svc.HandleWebhook(ctx, "sandbox", payload, sig); err != nil {
		t.Fatalf("HandleWebhook(paid): %v", err)
	}
	after := balanceOf()
	if diff := after - before; diff < 49_999.99 || diff > 50_000.01 {
		t.Fatalf("wallet balance moved by %.2f, want 50000", diff)
	}

	// The provider retries until it gets a 2xx, and the handler returned 2xx.
	// Replaying the exact delivery must be a no-op.
	if err := svc.HandleWebhook(ctx, "sandbox", payload, sig); err != nil {
		t.Fatalf("HandleWebhook(replay): %v", err)
	}
	if replay := balanceOf(); replay != after {
		t.Fatalf("a replayed webhook credited again: %.2f -> %.2f", after, replay)
	}

	// A forged signature must move nothing.
	if err := svc.HandleWebhook(ctx, "sandbox", payload, "deadbeef"); err == nil {
		t.Fatal("HandleWebhook accepted a forged signature")
	}
	if forged := balanceOf(); forged != after {
		t.Fatalf("a forged webhook moved money: %.2f -> %.2f", after, forged)
	}

	// The credit and its journal are one event. A wallet balance with no
	// journal is the drift the reconciliation job exists to find.
	var journals int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ledger_journals WHERE idempotency_key = 'topup:' || $1`, topUp.ID,
	).Scan(&journals); err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if journals != 1 {
		t.Fatalf("top-up wrote %d journals, want exactly 1", journals)
	}

	var debits, credits float64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'debit'), 0)::float8,
		       COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'credit'), 0)::float8
		  FROM ledger_entries e
		  JOIN ledger_journals j ON j.id = e.journal_id
		 WHERE j.idempotency_key = 'topup:' || $1`, topUp.ID,
	).Scan(&debits, &credits); err != nil {
		t.Fatalf("read journal legs: %v", err)
	}
	if debits != credits || debits < 49_999.99 {
		t.Fatalf("top-up journal is not balanced: debit %.2f credit %.2f", debits, credits)
	}
}
