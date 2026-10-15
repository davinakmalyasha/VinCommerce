package service

import (
	"strings"
	"testing"
)

func confirmExternalBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "order_service.go")
	at := strings.Index(src, "func (s *OrderService) ConfirmExternalPayment(")
	if at < 0 {
		t.Fatal("ConfirmExternalPayment is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}

func recordExternalCaptureBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "payment_service.go")
	at := strings.Index(src, "func (s *PaymentService) RecordExternalCapture(")
	if at < 0 {
		t.Fatal("RecordExternalCapture is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}

// THE DEFECT.
//
// `ConfirmExternalPayment` flipped the order to `paid` and wrote nothing else: no
// payment intent, no journal. `ReleaseEscrow` resolves the intent through
// `IntentByOrder`, so an order confirmed here was paid with no money path behind it
// and could never be settled:
//
//   - on the buyer's completion route the order moved to `completed` FIRST and the
//     release failed after, so the seller was never paid and nothing retried it
//   - on the sweep route the release is attempted first, so it retried forever and
//     the order never completed
func TestConfirmExternalPaymentBooksAnIntent(t *testing.T) {
	body := confirmExternalBody(t)

	if !strings.Contains(body, "RecordExternalCapture(") {
		t.Errorf("ConfirmExternalPayment does not book the payment.\n%s\n"+
			"Without an intent the order is `paid` but cannot be released, so it can "+
			"neither complete nor pay the seller", body)
	}
	// Order matters only in that both must be in the same transaction; assert the
	// booking happens before the status flip so a failure leaves the order pending.
	bookAt := strings.Index(body, "RecordExternalCapture(")
	flipAt := strings.Index(body, "MarkExternalPaid(")
	if bookAt < 0 || flipAt < 0 {
		t.Fatal("could not find both the booking and the status flip")
	}
	if bookAt > flipAt {
		t.Errorf("the payment is booked AFTER the order is flipped to paid (book %d, flip %d).\n"+
			"A failure in between would leave an order marked paid with no intent again", bookAt, flipAt)
	}
}

// The intent has to look like every other captured payment, or escrow, commission
// and refunds will treat it differently from the money it represents.
func TestExternalCaptureCreatesACapturedIntentLikeEveryOtherPath(t *testing.T) {
	body := recordExternalCaptureBody(t)

	for _, want := range []struct{ what, needle string }{
		{"the shared intent writer (so the rate card is snapshotted now)", ".CreateIntentTx("},
		{"the guarded status transition to captured", ".SetIntentStatusGuardedTx("},
		{"the manual-transfer method", "domain.MethodManualTransfer"},
		{"the external gateway", `Gateway: "external"`},
	} {
		if !strings.Contains(body, want.needle) {
			t.Errorf("the external capture does not use %s.\n%s", want.what, body)
		}
	}
	if !strings.Contains(body, "domain.IntentInitiated}, domain.IntentCaptured") {
		t.Errorf("the intent is not moved initiated -> captured.\n%s\n"+
			"An uncaptured intent is not releasable, so the order would stay stuck "+
			"exactly as before", body)
	}
}

// Strict, not the swallowing variant. A payment booked with no journal is the
// omission being fixed, and `postLedger` tolerates exactly that.
func TestExternalCapturePostsItsJournalStrictly(t *testing.T) {
	body := recordExternalCaptureBody(t)

	if !strings.Contains(body, "postLedgerStrict(") {
		t.Errorf("the external capture does not post its journal strictly.\n%s\n"+
			"The non-strict variant logs and continues, so a booked payment with no "+
			"journal would still be accepted", body)
	}
	if strings.Contains(body, "s.postLedger(") {
		t.Errorf("the external capture also calls the swallowing postLedger.\n%s", body)
	}
	// The key must be namespaced like the other capture journals, so the same
	// payment cannot be journalled twice under two different keys.
	if !strings.Contains(body, `IdempotencyKey: "capture:" + intent.ID`) {
		t.Errorf("the journal key is not the capture key for this intent.\n%s\n"+
			"Replay protection is by key; a different one double-books the payment", body)
	}
}
