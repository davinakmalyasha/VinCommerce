package service

import (
	"strings"
	"testing"
)

func completeOrderBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "order_service.go")
	at := strings.Index(src, "func (s *OrderService) CompleteOrder(")
	if at < 0 {
		t.Fatal("CompleteOrder is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}

func ensureEscrowReleasedBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "payment_service.go")
	at := strings.Index(src, "func (s *PaymentService) EnsureEscrowReleased(")
	if at < 0 {
		t.Fatal("EnsureEscrowReleased is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}

// THE DEFECT.
//
// CompleteOrder transitioned the order to `completed` and only THEN attempted the
// escrow release. A release that could not happen therefore left the order
// completed, the seller unpaid, and NOTHING to retry it -- the sweep selects
// `delivered` orders, and this one had already left that state. That is the silent
// half of the external-payment defect: the order looked finished and the money had
// not moved.
func TestCompleteOrderPaysTheSellerBeforeCompleting(t *testing.T) {
	body := completeOrderBody(t)

	payAt := strings.Index(body, "EnsureEscrowReleased(")
	transitionAt := strings.Index(body, "domain.OrderDelivered, domain.OrderCompleted")
	if payAt < 0 {
		t.Fatal("CompleteOrder does not release escrow through EnsureEscrowReleased")
	}
	if transitionAt < 0 {
		t.Fatal("the completion transition is gone; update this test")
	}
	if payAt > transitionAt {
		t.Errorf("CompleteOrder completes BEFORE paying (pay %d, transition %d).\n%s\n"+
			"A release that then fails leaves the order completed and the seller "+
			"unpaid, with no state left for the sweep to retry", payAt, transitionAt, body)
	}
	// Points were awarded before the release was even attempted, so an order whose
	// escrow failed still earned them.
	pointsAt := strings.Index(body, "awardPoints(")
	if pointsAt < 0 {
		t.Fatal("awardPoints is gone; update this test")
	}
	if pointsAt < payAt {
		t.Errorf("points are awarded before the seller is paid (points %d, pay %d).\n%s",
			pointsAt, payAt, body)
	}
}

// Paying first introduces the mirror hazard: a successful release followed by a
// failed transition leaves a `delivered` order whose escrow is already gone, and a
// plain retry would be refused at ESCROW_NOT_HELD for ever.
//
// This is what makes the retry safe, and it is the whole reason the helper exists
// rather than calling ReleaseEscrow directly.
func TestEnsureEscrowReleasedIsSafeToRetry(t *testing.T) {
	body := ensureEscrowReleasedBody(t)

	releasedAt := strings.Index(body, "domain.IntentReleased")
	releaseAt := strings.Index(body, "s.ReleaseEscrow(")
	if releasedAt < 0 {
		t.Errorf("EnsureEscrowReleased does not recognise an already-released intent.\n%s\n"+
			"A retry after a successful release must be a no-op, not ESCROW_NOT_HELD", body)
	}
	if releaseAt < 0 {
		t.Fatal("EnsureEscrowReleased never releases; update this test")
	}
	if releasedAt > releaseAt {
		t.Errorf("the already-released check runs AFTER the release call (check %d, release %d).\n"+
			"It has to short-circuit first, or it is not a guard at all", releasedAt, releaseAt)
	}
	if !strings.Contains(body, "return nil") {
		t.Errorf("EnsureEscrowReleased does not return success for an already-released intent.\n%s", body)
	}
}

// The sweep had the same wedge and the same fix applies: it paid first, so a
// release that succeeded and a transition that failed left the order retried
// against an intent that was already released.
func TestTheSweepAlsoUsesTheRetrySafeRelease(t *testing.T) {
	src := readSource(t, "order_service.go")
	at := strings.Index(src, "func (s *OrderService) CompleteDelivered(")
	if at < 0 {
		t.Fatal("CompleteDelivered is gone; update this test")
	}
	rest := src[at+1:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		t.Fatal("could not bound CompleteDelivered")
	}
	body := src[at : at+1+end]

	if strings.Contains(body, "s.payments.ReleaseEscrow(") {
		t.Errorf("CompleteDelivered still calls ReleaseEscrow directly.\n%s\n"+
			"A retry after a successful release is refused at ESCROW_NOT_HELD, so the "+
			"order is retried for ever without completing", body)
	}
	if !strings.Contains(body, "EnsureEscrowReleased(") {
		t.Errorf("CompleteDelivered does not use the retry-safe release.\n%s", body)
	}
}
