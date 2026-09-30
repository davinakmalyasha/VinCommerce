package service

import (
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// Every refund path must leave a `refunds` row, because that table is what the
// cumulative cap reads.
//
// The return path did not. `refundInTx` -- reached from
// `SellerService.RefundReturn` -> `RefundOrderInTx` -- locked the intent, derived
// the capped plan, debited the seller, debited the platform's commission, credited
// the buyer and posted a ledger journal. It wrote no row. The money moved and the
// cap could not see it:
//
//	return one Rp50,000 item of a Rp100,000 order  -> no row; cap reads 0
//	gateway refund the same order                  -> cap reads 0, ALLOWS Rp100,000
//	                                               -> Rp150,000 out on Rp100,000
//
// No race, no provider retry, no double webhook. It is what an ordinary returned
// item followed by an ordinary refund does.
//
// These are the two halves that a fix needs, and they are tested separately on
// purpose:
//
//   - that a `refunds` row bounds the cap, driven through the real arithmetic
//   - that the return path actually writes one, which is not reachable without a
//     live database and is therefore pinned at the source
//
// Testing only the first would pass against code that still writes nothing.

// A refund already recorded for the order must reduce what is left, whatever path
// recorded it. This drives the real cap; the row is the return refund's.
func TestARecordedReturnRefundReducesWhatIsStillRefundable(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	// The return path's row: Rp50,000 of a Rp100,000 order, already settled.
	db := &fakeRefundsDB{rows: []fakeRefundRow{
		{id: "ret-1", amount: 50000, status: RefundStateSucceeded},
	}}

	// The whole charge must now be refused: only Rp50,000 is left.
	_, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 100000, nil)
	if err == nil {
		t.Fatal("a Rp100,000 refund was approved on an order that had already " +
			"returned a Rp50,000 item; the cap cannot see that refund")
	}
	if !strings.Contains(err.Error(), "exceeds the remaining refundable") {
		t.Errorf("error = %v, want REFUND_EXCEEDS_REMAINING naming the Rp50,000 left", err)
	}

	// And the genuine remainder is still refundable. A cap that refuses everything
	// is not a cap, and the buyer is owed this.
	plan, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 50000, nil)
	if err != nil {
		t.Fatalf("the remaining Rp50,000 was refused: %v", err)
	}
	if plan.Amount != 50000 {
		t.Errorf("amount = Rp%.0f, want Rp50,000", plan.Amount)
	}
	if plan.Partial {
		t.Error("partial = true, but nothing remains after this")
	}
	if plan.NextStatus != domain.IntentRefunded {
		t.Errorf("next status = %q, want %q", plan.NextStatus, domain.IntentRefunded)
	}
}

// The path that moves money must write the row. Pinned at the source because
// `refundInTx` runs inside a caller-owned transaction and cannot be entered
// without a live database -- the same limitation source_assert_test.go documents,
// and the reason M16 is caught at all.
//
// The status is asserted as well as the call, because a row written as `pending`
// or `failed` exists and still does not count: it would make the table look
// authoritative while the cap kept reading zero.
func TestTheReturnRefundPathWritesAnAuthoritativeRefundRow(t *testing.T) {
	body := functionSource(t, "payment_service.go", "refundInTx")

	if !strings.Contains(body, "s.payments.CreateRefund(ctx, q, refund)") {
		t.Errorf("refundInTx does not write a refunds row:\n%s\n"+
			"it moves the money and posts the journal, but the cumulative cap reads "+
			"the `refunds` table -- so a returned item is invisible to it and a later "+
			"refund on the same order is capped against a total that excludes it",
			body)
	}
	if !strings.Contains(body, "Status:          RefundStateSucceeded") {
		t.Errorf("the return path's row is not recorded as succeeded:\n%s\n"+
			"the money has already moved, so `succeeded` is the truthful state. A "+
			"row written as `pending` or `failed` does not count toward the cap, "+
			"which would leave the table authoritative in name only", body)
	}
	if !strings.Contains(body, "Amount:          plan.Amount") {
		t.Errorf("the return path's row does not record the amount actually refunded:\n%s\n"+
			"a row that disagrees with the money movement is worse than no row, "+
			"because the cap would then trust it", body)
	}
	// The row must be written INSIDE the caller's transaction, so it commits with
	// the money or not at all.
	if strings.Contains(body, "s.payments.Pool()") {
		t.Errorf("the return path's row is written outside the caller's transaction:\n%s\n"+
			"a row that commits independently of the money it describes can outlive "+
			"a rolled-back refund and cap every future refund on the order", body)
	}
}
