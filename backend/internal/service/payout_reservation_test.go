package service

import (
	"strings"
	"testing"
)

// A seller hold is only worth taking if something takes it.
//
// The release job added in the previous commit releases `cod`, `return` and
// `dispute` holds once the lag passes with no open return or dispute. That
// release rule was written against holds that NOTHING CREATED: the only
// `seller_reservations` rows in the system were `payout` rows, written when a
// seller requested a withdrawal. So the fraud control was a release schedule for
// holds that did not exist, and the exposure it was built for -- a seller
// withdrawing money a return is about to reverse -- was entirely unguarded.
//
// This is the same shape as the cart-recovery job that had no mailer, and the
// reconciliation jobs that were implemented, scheduled and never wired: the
// mechanism is present and the thing that drives it is not. A guard with no input
// is not a guard, and a release rule with no holds is bookkeeping about nothing.
//
// So the assertions are deliberately structural rather than behavioural. Both call
// sites open their own repository transactions and cannot be entered without a
// live database, and what matters here is not arithmetic -- it is that the call
// exists, names the right kind, and appears BEFORE the row it protects.

// A return claim holds back the ITEM's value, and holds it before the claim is
// recorded.
func TestAReturnClaimHoldsTheItemValueBeforeItIsRecorded(t *testing.T) {
	body := functionSource(t, "seller_service.go", "RequestReturn")

	holdAt := strings.Index(body, "HoldSellerFunds(")
	if holdAt < 0 {
		t.Fatalf("RequestReturn takes no seller hold:\n%s\n"+
			"a return reverses the seller's money, and a seller who withdraws it in "+
			"the meantime leaves the refund unable to pay the buyer", body)
	}
	recordAt := strings.Index(body, "s.stores.CreateReturn(")
	if recordAt < 0 {
		t.Fatalf("RequestReturn does not record the claim:\n%s", body)
	}
	if holdAt > recordAt {
		t.Errorf("the claim is recorded before the hold is taken:\n%s\n"+
			"a stray hold self-releases after the lag, but a claim recorded without "+
			"one leaves the money withdrawable and the reversal with nothing to take",
			body)
	}
	if !strings.Contains(body, "HoldReturn") {
		t.Errorf("the hold is not a return hold:\n%s\n"+
			"the release job evaluates kinds by name, and a hold it does not "+
			"recognise is a hold that is never released", body)
	}
	// The ITEM's value, not the order's: a return reverses one item, and holding
	// the whole order would freeze money the seller is entitled to keep.
	if !strings.Contains(body, "item.Total") {
		t.Errorf("the hold is not the item's value:\n%s\n"+
			"holding the order total would make an Rp50,000 return on a Rp500,000 "+
			"order freeze Rp500,000, and the seller could not withdraw the other "+
			"Rp450,000 they are owed", body)
	}
	assertHoldIsGuardedByAWiringCheck(t, body, "s.paymentSvc")
}

// assertHoldIsGuardedByAWiringCheck requires the hold to sit behind a nil-check on
// the payment service, and that the check is not a literal false.
//
// This exists because of a mutation that SURVIVED the ordering assertion above:
// rewriting `if s.paymentSvc != nil` to `if false` leaves the hold's text exactly
// where it was, so a test that only checks source POSITION cannot see that the
// branch is now dead. A guard that has been disabled while leaving its text in
// place is the same failure as a guard that was never written, and it is the reason
// this assertion looks at the condition rather than the distance.
//
// WHAT THIS STILL CANNOT SEE. A source assertion cannot prove the branch is
// reachable at runtime. Both call sites open their own repository transactions, so
// driving them needs a live database, and until 00043 has executed there is no way
// to observe one. This pins the code's shape; it does not witness its execution.
func assertHoldIsGuardedByAWiringCheck(t *testing.T, body, field string) {
	t.Helper()
	holdAt := strings.Index(body, "HoldSellerFunds(")
	if holdAt < 0 {
		t.Fatal("no hold to check")
	}
	// The nearest `if` before the call is the guard around it.
	guard := body[:holdAt]
	if i := strings.LastIndex(guard, "\n\tif "); i >= 0 {
		guard = guard[i+1:]
	} else {
		return // unconditional: nothing to assert
	}
	if !strings.Contains(guard, field) {
		t.Errorf("the hold is not guarded by a check on %s:\n%s\n"+
			"an unwired payment service would panic here rather than skipping the "+
			"hold, and a seller hold is not worth crashing a return request over",
			field, guard)
	}
	if strings.Contains(guard, "if false") {
		t.Errorf("the hold is behind a literal false:\n%s\n"+
			"the code is present and unreachable, which is how a guard survives a "+
			"review and a test suite while doing nothing", guard)
	}
}

// A dispute holds back the order, and holds it before the dispute is recorded.
func TestAnOpenDisputeHoldsTheOrderBeforeItIsRecorded(t *testing.T) {
	body := functionSource(t, "market_service.go", "OpenDispute")

	holdAt := strings.Index(body, "HoldSellerFunds(")
	if holdAt < 0 {
		t.Fatalf("OpenDispute takes no seller hold:\n%s\n"+
			"a dispute resolves in the buyer's favour often enough to matter, and "+
			"the money has to still be there when it does", body)
	}
	recordAt := strings.Index(body, "s.disputes.Create(")
	if recordAt < 0 {
		t.Fatalf("OpenDispute does not record the dispute:\n%s", body)
	}
	if holdAt > recordAt {
		t.Errorf("the dispute is recorded before the hold is taken:\n%s\n"+
			"a recorded dispute with no hold behind it is an exposure with nothing "+
			"protecting it", body)
	}
	if !strings.Contains(body, "HoldDispute") {
		t.Errorf("the hold is not a dispute hold:\n%s", body)
	}
	// The order, not an item: a dispute is about the order.
	if !strings.Contains(body, "order.Amount") {
		t.Errorf("the hold is not the order's amount:\n%s", body)
	}
	assertHoldIsGuardedByAWiringCheck(t, body, "s.payments")
}

// A hold whose kind the release job does not evaluate is a hold that is never
// released, which is a permanent deduction from a seller's balance. So the two
// vocabularies must be the same one.
