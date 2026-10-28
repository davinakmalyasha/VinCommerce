package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// The whole claim lifecycle against a real database: submit, duplicate, reject,
// resubmit, approve, decide-twice.
//
// The security property this protects is the one the old endpoint violated: a buyer
// could mark their own order paid. Everything below is about proving that the ONLY
// thing that moves an order out of `pending` is an admin decision, and that a
// rejected claim leaves the order exactly as it was.
func TestExternalPaymentClaimLifecycle(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	svc := NewOrderService(
		repository.NewCartRepository(pool),
		repository.NewOrderRepository(pool),
		repository.NewAddressRepository(pool),
	)
	svc.SetStores(repository.NewStoreRepository(pool))
	svc.SetPaymentClaims(repository.NewPaymentClaimRepository(pool))

	var buyerID, adminID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = 'buyer.sample@vincommerce.com'`).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = 'admin@vincommerce.com'`).Scan(&adminID); err != nil {
		t.Fatalf("seeded admin missing: %v", err)
	}

	var variantID string
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE p.slug = 'smartphone-aurora-x5-pro' AND v.name = 'Black'`).Scan(&variantID); err != nil {
		t.Fatalf("seeded variant missing: %v", err)
	}

	cart, err := svc.CartFor(ctx, buyerID, "")
	if err != nil {
		t.Fatalf("cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cart.ID); err != nil {
		t.Fatalf("clear cart: %v", err)
	}
	if _, _, err := svc.UpdateCartItem(ctx, cart.ID, variantID, 1); err != nil {
		t.Fatalf("seed cart: %v", err)
	}

	order, err := svc.PlaceOrder(ctx, PlaceOrderInput{
		UserID: buyerID,
		CartID: cart.ID,
		Address: &domain.Address{
			Recipient:    "Claim Integration",
			Phone:        "080000000000",
			AddressLine1: "Jl. Klaim No. 1",
			City:         "Bandung",
			Province:     "Jawa Barat",
			PostalCode:   "40115",
			Country:      "Indonesia",
		},
		ShippingMethodCode: "regular",
		IdempotencyKey:     "claim-integration-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	if len(order.Orders) != 1 {
		t.Fatalf("expected one order, got %d", len(order.Orders))
	}
	orderID := order.Orders[0].ID

	status := func() string {
		t.Helper()
		o, err := svc.orders.ByID(ctx, orderID)
		if err != nil {
			t.Fatalf("reload order: %v", err)
		}
		return o.Status
	}

	ref := "CLAIM-REF-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// 1. The buyer files a claim. The order must NOT move.
	claimID, err := svc.SubmitExternalPaymentClaim(ctx, orderID, buyerID, ref, order.GrandTotal, time.Time{})
	if err != nil {
		t.Fatalf("submit claim: %v", err)
	}
	if got := status(); got != domain.OrderPending {
		t.Fatalf("a claim moved the order to %q; only an admin decision may", got)
	}

	// 2. A second pending claim for the same order is a duplicate.
	_, err = svc.SubmitExternalPaymentClaim(ctx, orderID, buyerID, ref+"-dup", order.GrandTotal, time.Time{})
	if !hasCode(err, "CLAIM_PENDING") {
		t.Fatalf("duplicate claim error = %v, want CLAIM_PENDING", err)
	}

	// 3. Rejection leaves the order pending and recorded as rejected.
	if err := svc.RejectExternalPaymentClaim(ctx, adminID, claimID, "reference not found"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got := status(); got != domain.OrderPending {
		t.Fatalf("rejection moved the order to %q", got)
	}

	// 4. A decided claim cannot be decided again.
	if err := svc.ApproveExternalPaymentClaim(ctx, adminID, claimID); !hasCode(err, "CLAIM_DECIDED") {
		t.Fatalf("approving a rejected claim = %v, want CLAIM_DECIDED", err)
	}

	// 5. A rejected claim frees the order for corrected evidence.
	secondID, err := svc.SubmitExternalPaymentClaim(ctx, orderID, buyerID, ref+"-2", order.GrandTotal, time.Time{})
	if err != nil {
		t.Fatalf("resubmit after rejection: %v", err)
	}
	if secondID == claimID {
		t.Fatal("the resubmitted claim reused the rejected one")
	}

	// 6. Approval is what pays the order.
	if err := svc.ApproveExternalPaymentClaim(ctx, adminID, secondID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := status(); got != domain.OrderPaid {
		t.Fatalf("order status = %q after approval, want paid", got)
	}

	// 7. And it cannot be done twice.
	if err := svc.ApproveExternalPaymentClaim(ctx, adminID, secondID); !hasCode(err, "CLAIM_DECIDED") {
		t.Fatalf("double approval = %v, want CLAIM_DECIDED", err)
	}
}

func hasCode(err error, code string) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == code
}
