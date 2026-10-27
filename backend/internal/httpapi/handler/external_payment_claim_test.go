package handler

import (
	"os"
	"strings"
	"testing"
)

func handlerFuncBody(t *testing.T, file, signature string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	src := string(raw)
	at := strings.Index(src, signature)
	if at < 0 {
		t.Fatalf("%s is gone; update this test", signature)
	}
	rest := src[at:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		return rest[:1+end]
	}
	return rest
}

// The buyer may file a CLAIM and nothing more.
//
// The endpoint used to call ConfirmExternalPayment, which marked the order paid on the
// buyer's own say-so: a reference string and an amount equal to the total were treated
// as proof that money had arrived. Any buyer could pay their own order with a
// fabricated reference.
//
// This pins the split in the place it was wrong. It is structural because the service
// methods need a database to exercise; the integration test covers the behaviour.
func TestBuyerExternalPaymentSubmitsAClaimOnly(t *testing.T) {
	body := handlerFuncBody(t, "cart.go", "func (h *Orders) ExternalPayment(")

	if strings.Contains(body, "ConfirmExternalPayment(") {
		t.Fatal("the buyer endpoint confirms the payment again: a claim is not a receipt")
	}
	if !strings.Contains(body, "SubmitExternalPaymentClaim(") {
		t.Fatal("the buyer endpoint does not record a claim")
	}
	if !strings.Contains(body, "StatusAccepted") {
		t.Error("the endpoint should answer 202 Accepted: the claim is accepted for " +
			"review, not confirmed")
	}
}

// Approval is the ONLY route from a claim to a paid order, and it confirms the
// claim's own figures rather than accepting fresh ones at approval time.
func TestApprovalConfirmsTheClaimsOwnFigures(t *testing.T) {
	body := handlerFuncBody(t, "payment_claims.go", "func (h *AdminClaims) Decide(")
	if !strings.Contains(body, `"approved"`) || !strings.Contains(body, `"rejected"`) {
		t.Fatal("the decision endpoint no longer accepts both outcomes")
	}

	svc := readServiceBody(t, "func (s *OrderService) ApproveExternalPaymentClaim(")
	if !strings.Contains(svc, "ConfirmExternalPayment(") {
		t.Fatal("approval no longer runs the paid-transition: a claim would be marked " +
			"approved without the payment ever being booked")
	}
	for _, want := range []string{"c.OrderID", "c.BuyerID", "c.Reference", "c.Amount"} {
		if !strings.Contains(svc, want) {
			t.Fatalf("approval does not pass %s from the claim; it must confirm the "+
				"evidence the admin was shown", want)
		}
	}
}

func readServiceBody(t *testing.T, signature string) string {
	t.Helper()
	raw, err := os.ReadFile("../../service/order_service.go")
	if err != nil {
		t.Fatalf("read order_service.go: %v", err)
	}
	src := string(raw)
	at := strings.Index(src, signature)
	if at < 0 {
		t.Fatalf("%s is gone; update this test", signature)
	}
	rest := src[at:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		return rest[:1+end]
	}
	return rest
}
