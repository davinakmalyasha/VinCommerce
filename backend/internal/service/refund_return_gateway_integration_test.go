package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
)

// P0-1, end to end: a buyer who paid through the provider must be refunded
// through the provider. Before this, the return path credited the wallet even
// for a QRIS or bank-transfer payment -- money the buyer could not withdraw,
// and a Midtrans terms-of-service breach.
//
// The test builds its own intent and return on a seeded delivered order and
// removes them afterwards, so it is repeatable on a database that is not
// recreated between runs.
func TestReturnRefundGoesThroughTheGateway(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var orderID, buyerID, sellerID, itemID string
	var itemTotal float64
	err := pool.QueryRow(ctx, `
		SELECT o.id::text, o.buyer_id::text, o.seller_id::text, oi.id::text, oi.total::float8
		  FROM orders o
		  JOIN order_items oi ON oi.order_id = o.id
		 WHERE o.status IN ('delivered', 'completed')
		   AND NOT EXISTS (SELECT 1 FROM payment_intents pi WHERE pi.order_id = o.id)
		   AND NOT EXISTS (
		         SELECT 1 FROM return_requests r
		          WHERE r.order_item_id = oi.id
		            AND r.status IN ('requested', 'approved', 'refunding', 'returned'))
		 ORDER BY o.created_at DESC
		 LIMIT 1`).Scan(&orderID, &buyerID, &sellerID, &itemID, &itemTotal)
	if err != nil {
		if err == pgx.ErrNoRows {
			t.Skip("no delivered order without an intent or live return to use as a fixture")
		}
		t.Fatalf("find fixture order: %v", err)
	}

	// A captured, gateway-paid intent and an approved return. Created directly
	// rather than through checkout: this test is about RefundReturn, and driving
	// a whole checkout would add failure modes that have their own tests.
	var intentID string
	ref := "sbx_return_test_" + uuid.NewString()[:8]
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_intents
		       (id, order_id, buyer_id, amount, currency, status, gateway, gateway_ref, method, idempotency_key)
		VALUES (gen_random_uuid(), $1, $2,
		        (SELECT total_amount FROM orders WHERE id = $1), 'IDR', 'captured', 'sandbox', $3,
		        'e_wallet', $4)
		RETURNING id::text`, orderID, buyerID, ref, "intent-"+uuid.NewString()).
		Scan(&intentID); err != nil {
		t.Fatalf("insert intent: %v", err)
	}

	var returnID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests
		       (id, order_id, order_item_id, buyer_id, seller_id, reason, description, status, resolution)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 'defective', 'integration fixture', 'approved', 'refund')
		RETURNING id::text`, orderID, itemID, buyerID, sellerID).
		Scan(&returnID); err != nil {
		t.Fatalf("insert return: %v", err)
	}

	t.Cleanup(func() {
		// Order matters: the refunds row references the intent, and the return
		// references the order item. The event is written by the path under test.
		_, _ = pool.Exec(ctx, `DELETE FROM order_events WHERE order_id = $1 AND note LIKE '%' || $2 || '%'`, orderID, returnID)
		_, _ = pool.Exec(ctx, `DELETE FROM refunds WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(ctx, `DELETE FROM return_requests WHERE id = $1`, returnID)
		_, _ = pool.Exec(ctx, `DELETE FROM payment_intents WHERE id = $1`, intentID)
	})

	payRepo := repository.NewPaymentRepository(pool)
	ordersRepo := repository.NewOrderRepository(pool)
	paySvc := NewPaymentService(
		payRepo, ordersRepo,
		[]payments.Gateway{&payments.SandboxGateway{}},
		"sandbox", "http://localhost:8080")
	paySvc.SetLedger(NewLedgerService(repository.NewLedgerRepository(pool), nil))

	sellerSvc := NewSellerService(
		repository.NewStoreRepository(pool),
		repository.NewUserRepository(pool),
		repository.NewProductRepository(pool),
		ordersRepo, payRepo)
	sellerSvc.SetPaymentService(paySvc)

	walletBefore := func() float64 {
		t.Helper()
		var balance float64
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT balance FROM wallets WHERE user_id = $1), 0)::float8`,
			buyerID).Scan(&balance); err != nil {
			t.Fatalf("read wallet: %v", err)
		}
		return balance
	}
	before := walletBefore()

	// The seed has no proof-provider credentials; the sandbox adapter records the
	// refund as pending, which is exactly the asynchronous shape Midtrans has.
	if err := sellerSvc.RefundReturn(ctx, returnID, "integration test"); err != nil {
		t.Fatalf("RefundReturn: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, returnID).Scan(&status); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if status != "refunded" {
		t.Fatalf("return status = %q, want refunded", status)
	}

	var refundStatus string
	var refundAmount float64
	if err := pool.QueryRow(ctx, `
		SELECT status, amount::float8 FROM refunds
		 WHERE order_id = $1 AND reason = $2`,
		orderID, "return refund: "+returnID).Scan(&refundStatus, &refundAmount); err != nil {
		t.Fatalf("no refunds row for the return: %v", err)
	}
	if refundAmount != itemTotal {
		t.Fatalf("refunded %.0f, want the item total %.0f", refundAmount, itemTotal)
	}
	if refundStatus == RefundStateFailed {
		t.Fatalf("refund row recorded as failed")
	}

	if after := walletBefore(); after != before {
		t.Fatalf("the buyer's wallet was credited %.2f by a gateway refund (before %.2f, after %.2f); "+
			"a provider-paid buyer must be refunded through the provider",
			after-before, before, after)
	}
}
