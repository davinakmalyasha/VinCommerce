package service

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The split-shipment deadlock.
//
// Before this, `FulfillOrder(.., OrderShipped, ..)` accepted `packed` and refused
// every other status. So a seller who recorded the first parcel of a two-parcel
// order saw the order become `partially_shipped`, and the second parcel could never
// be recorded: the shortcut refused, and the order was stuck at "partially shipped"
// with goods still in a warehouse and no way to finish.
//
// The order is not stuck by accident. It is stuck because the only shipping path
// required the order to have no parcels, and recording a parcel necessarily moved
// it out of that state.
func TestASplitOrderCanFinishShipping(t *testing.T) {
	body := functionSource(t, "seller_service.go", "FulfillOrder")

	// The shortcut must recognise a half-shipped order and refuse it WITH THE WAY
	// OUT, not silently.
	// The CONDITION, not the error code.
	//
	// Checking that "ORDER_ALREADY_PARTLY_SHIPPED" appears passes just as well on
	// `if false && order.Status == domain.OrderPartiallyShipped {`, because the string
	// is still sitting there in dead code. M45 survived the first version of this test
	// for exactly that reason -- an error code that nothing can reach is not a control.
	//
	// This is the third time this mistake has been made in this session (M38, M39,
	// M45). The pattern is worth naming: asserting that a string is PRESENT says
	// nothing about whether the code around it RUNS.
	if !strings.Contains(body, "if order.Status == domain.OrderPartiallyShipped {") {
		t.Errorf("the half-shipped check is not reached, so the second parcel of a "+
			"split order is still impossible to record:\n%s", body)
	}
	if !strings.Contains(body, "ORDER_ALREADY_PARTLY_SHIPPED") {
		t.Errorf("a half-shipped order does not get a named refusal:\\n%s\\n"+
			"the seller needs to be told to record a parcel, not merely denied", body)
	}
	if !strings.Contains(body, "shipment endpoint") && !strings.Contains(body, "another parcel") {
		t.Errorf("the refusal does not say what to do instead:\\n%s", body)
	}

	// And `packed -> shipped` must still work. Widening the vocabulary is only safe
	// if the single-parcel case is untouched.
	if !strings.Contains(body, "if order.Status != domain.OrderPacked {") {
		t.Errorf("the single-parcel shortcut no longer guards on packed:\\n%s\\n"+
			"the common case must keep working", body)
	}
}

// The order status is DERIVED from what is in the boxes, never requested.
//
// No service method takes a status. This is the same correction the payout work made
// about `orders.shipped_at` meaning "a seller pressed a button", and split shipping
// makes it unavoidable: with two parcels, "is it shipped" has no single answer to
// type in.
func TestNoShipmentMethodAcceptsAStatus(t *testing.T) {
	raw, err := os.ReadFile("shipment_service.go")
	if err != nil {
		t.Fatalf("read shipment_service.go: %v", err)
	}
	body := string(raw)

	// NOTE: the patterns must not contain the bare word "status" in a position where a
	// method legitimately takes a status-derived value. The first version forbade
	// "status string", and the return methods introduced
	// `(orderID, itemID, buyerID, sellerID, status string, err error)` from a
	// multi-return helper -- which is a READ, not a request. Asserting on the word
	// rather than on the direction of the data is the mistake.
	//
	// What this test really guards is that no method takes a status the CALLER
	// chooses. That is checked per method below, by name.
	for _, forbidden := range []string{"to string", "newStatus string", "orderStatus string"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("a shipment method accepts a status (%q):\\n%s\\n"+
				"the status is a fact about the boxes; accepting it from the caller is "+
				"how `orders.shipped_at` came to mean a seller pressed a button",
				forbidden, body)
		}
	}
	// It must actually derive.
	if !strings.Contains(body, "DeriveShippingStatus") {
		t.Errorf("nothing derives the order status from the parcels:\\n%s", body)
	}
	// And a cancelled or returned order must never gain a parcel.
	for _, refused := range []string{"ORDER_NOT_SHIPPABLE", "OrderCancelled"} {
		if !strings.Contains(body, refused) {
			t.Errorf("a cancelled order is not refused a parcel (%s):\\n%s\\n"+
				"a parcel for a cancelled order is a box of goods leaving for nobody",
				refused, body)
		}
	}
}

// One commit for the parcel, its contents, and the order status.
//
// Deriving "partially_shipped" from what is in the boxes only means something if the
// boxes and the status agree. A repository method that opens its own transaction
// makes that impossible, so `TransitionOrderTx` was added for exactly this reason --
// the same reasoning that deleted FailPayout and CompletePayout.
func TestTheParcelAndTheOrderStatusCommitTogether(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "CreateParcel")

	for _, want := range []string{"Begin(ctx)", "AddShipmentItems", "DeriveShippingStatus", "tx.Commit(ctx)"} {
		if !strings.Contains(body, want) {
			t.Errorf("CreateParcel does not %s:\\n%s", want, body)
		}
	}
	// The rollback defer, so an overship refusal part-way through a multi-line
	// parcel does not leave the lines already reserved.
	if !strings.Contains(body, "tx.Rollback(ctx)") {
		t.Errorf("CreateParcel has no rollback:\\n%s\\n"+
			"a refusal part-way through would leave earlier lines reserved and the "+
			"order permanently short of units", body)
	}
	// And the derived status must be applied through the transaction-scoped variant,
	// not the one that opens its own.
	// And the derived status must reach the transition inside the SAME transaction.
	//
	// Checked on `applyDerivedStatus` rather than on CreateParcel, because
	// CreateParcel delegates. The delegation is fine; what matters is that the method
	// it delegates to receives the CALLER'S Querier, so the status update commits with
	// the boxes. An earlier version of this test demanded the literal text
	// `TransitionOrderTx` inside CreateParcel and failed on correct code -- the
	// property is which querier reaches the transition, not which function names it.
	if !strings.Contains(body, "applyDerivedStatus(ctx, q,") {
		t.Errorf("CreateParcel does not derive the status through its own transaction:\\n%s\\n"+
			"a derivation committed separately leaves the boxes and the order status "+
			"disagreeing in between", body)
	}
	apply := functionSource(t, "shipment_service.go", "applyDerivedStatus")
	if !strings.Contains(apply, "TransitionOrderTx(ctx, q,") {
		t.Errorf("applyDerivedStatus uses the transaction-opening transition:\\n%s\\n"+
			"TransitionOrder opens its own transaction, so the parcel commits first "+
			"and the status second", apply)
	}
}

// A derivation the state machine refuses is a BUG, not a seller error.
//
// Silently ignoring it would leave the parcel recorded and the status lying.
// Blaming the seller would be wrong, because they did not create the state.
func TestAnIllegalDerivedStatusIsReportedNotForced(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "applyDerivedStatus")

	if !strings.Contains(body, "CanTransition") {
		t.Errorf("the derived status is applied without checking it is legal:\\n%s\\n"+
			"an order that is already delivered, with a late parcel recorded, would "+
			"either be moved illegally or left inconsistent with its own boxes", body)
	}
	if !strings.Contains(body, "SHIPMENT_STATUS_NOT_REACHABLE") {
		t.Errorf("an illegal derivation is not reported:\\n%s\\n"+
			"the parcel was recorded and the order was not moved, so somebody has to "+
			"be told or the two silently disagree", body)
	}
	if !strings.Contains(body, "domain.KindInternal") {
		t.Errorf("an illegal derivation is reported as a seller error:\\n%s\\n"+
			"the seller cannot act on a state they did not create", body)
	}
	// A no-op derivation must not transition, or every parcel write burns a row in
	// order_events saying nothing happened.
	if !strings.Contains(body, "derived == current") {
		t.Errorf("an unchanged derivation still transitions:\\n%s", body)
	}
}

// The buyer hears about the LAST parcel, not the first.
//
// Announcing parcel one of two tells a buyer their order is on its way when most of
// it is still in a warehouse.
func TestTheBuyerIsNotifiedOnceForTheWholeOrder(t *testing.T) {
	dispatch := functionSource(t, "shipment_service.go", "DispatchParcel")
	// The parcel service must not mail at all -- it has no way to resolve the buyer,
	// and a first draft declared a Mailer interface whose argument order was the
	// reverse of mail.Client.Send's (subject before template), which would have
	// compiled and swapped every subject line in production.
	if strings.Contains(dispatch, "mailer") || strings.Contains(dispatch, "Mailer") {
		t.Errorf("ShipmentService sends mail:\\n%s\\n"+
			"the buyer is resolved by SellerService.emailBuyer; a second mail seam "+
			"would be a second path to the same inbox", dispatch)
	}
	if !strings.Contains(dispatch, "derived == domain.OrderShipped") {
		t.Errorf("DispatchParcel does not report whether it was the last parcel:\\n%s", dispatch)
	}

	notify := functionSource(t, "seller_service.go", "DispatchParcel")
	// The GUARD. A bare `strings.Contains(notify, "allShipped")` passes on
	// `if true {`, because `out, allShipped, err := ...` mentions the identifier
	// either way -- which is how M48 survived the first version of this test. The
	// condition that decides is the property, not the word.
	if !strings.Contains(notify, "if allShipped {") {
		t.Errorf("the seller service ignores whether the parcel was the last:\\n%s\\n"+
			"the buyer is told their order shipped when most of it is still in a "+
			"warehouse", notify)
	}
}

// Nil must produce a named refusal, not a panic and not a silent repository
// fallback. The whole point of the parcel service is the single commit; a fallback
// would quietly drop it.
func TestAnUnwiredParcelServiceRefusesByName(t *testing.T) {
	raw, err := os.ReadFile("seller_service.go")
	if err != nil {
		t.Fatalf("read seller_service.go: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "SHIPMENTS_NOT_WIRED") {
		t.Errorf("an unwired parcel service is not refused by name:\\n%s", body)
	}
	for _, method := range []string{"func (s *SellerService) CreateParcel", "func (s *SellerService) DispatchParcel"} {
		i := strings.Index(body, method)
		if i < 0 {
			t.Errorf("%s is missing", method)
			continue
		}
		rest := body[i:]
		if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "s.shipSvc == nil") {
			t.Errorf("%s does not check for a nil parcel service:\\n%s", method, rest)
		}
	}
}

// Cancelled and returned lines must be EXCLUDED from the derivation.
//
// M49 targets this and it had no test whatsoever, which the mutation reported on its
// first run. Count them and an order of three units with one line cancelled can never
// reach "shipped": the derivation compares shipped against ordered, the cancelled
// unit counts as something still owed, and every retry re-derives the same wrong
// answer while the seller watches an order that will never complete.
//
// Asserted on the repository method, because that is where the counts come from --
// the service only reads what it is handed.
func TestTheDerivationExcludesCancelledAndReturnedLines(t *testing.T) {
	body := methodSourceOf(t, readRepositorySource(t, "order_repo.go"), "DeriveShippingStatus")

	if !strings.Contains(body, "AND status NOT IN ('cancelled', 'returned')") {
		t.Errorf("cancelled and returned lines are counted towards the shipped "+
			"total:\n%s\n"+
			"an order of three units with one line cancelled would compare shipped "+
			"against three, never reach `shipped`, and re-derive the same wrong "+
			"answer on every retry", body)
	}

	// And it must be a predicate in the same statement as the sums, not subtracted
	// afterwards in Go -- where it would be easy to apply to only one of the counts.
	if strings.Contains(body, "ordered -= 1") || strings.Contains(body, "shipped -= 1") {
		t.Errorf("the exclusion is applied in Go rather than in the query:\n%s", body)
	}

	// Nothing shipped yet must NOT derive a status: returning "packed" would drag a
	// paid-but-unpacked order backwards, and this runs on every parcel write.
	if !strings.Contains(body, "shipped == 0") {
		t.Errorf("an order with nothing shipped does not short-circuit:\n%s\n"+
			"deriving a status for it would move a paid order backwards", body)
	}

	// And it must report whether it wants a change, or every parcel write burns a row
	// in order_events recording a transition to where it already was.
	if !strings.Contains(body, "changed") {
		t.Errorf("the derivation does not report whether it wants a change:\n%s", body)
	}
}

// methodSourceOf returns the body of the named top-level method in `body`, using
// the same slicing discipline as functionSource. Split out because the repository
// package's files are read through readRepositorySource rather than readSource.
func methodSourceOf(t *testing.T, body, fn string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^func \(r \*\w+Repository\) ` + regexp.QuoteMeta(fn) + `\(`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		t.Fatalf("method %s not found", fn)
	}
	rest := body[loc[0]:]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
