package service

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// THE RETURN LABEL MUST GO TO THE SELLER, NOT THE BUYER.
//
// This is the single most damaging mistake available in this area, and it was made
// and caught in the same sitting. `returnDestinationFor` takes a `sellerID`, the call
// site passed `shipment.OrderID`, and THE COMPILER WAS HAPPY: both are strings.
//
// The failure is silent and total. The lookup is
//
//	SELECT return_address FROM stores WHERE owner_id = $1
//
// and an order id never matches a store owner, so every return label is refused with
// "no destination". The return feature is completely dead, and every test on the
// carrier package still passes because the carrier is behaving perfectly on an
// empty address.
//
// M64 pins it, and this is the regression test for the specific mistake.
func TestAReturnLabelGoesToTheSellerNotTheBuyer(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "BuyReturnLabel")

	if !strings.Contains(body, "returnDestinationFor(ctx, shipment.SellerID)") {
		t.Errorf("the return label destination is not the seller:\n%s\n"+
			"passing the ORDER id here compiles, silently never matches a store owner, "+
			"and makes every return label fail with `no destination`", body)
	}
	// And the buyer's address must not be consulted anywhere on this path, even as a
	// fallback -- a fallback that is sometimes right is a fallback that will be wrong
	// in production.
	if strings.Contains(body, "destinationFor(ctx, shipment.OrderID)") {
		t.Errorf("a return label falls back to the ORDER's shipping address:\n%s\n"+
			"that is the BUYER's address, so the returned goods are posted back to the "+
			"buyer at the seller's expense, as a second parcel of the same items", body)
	}

	// The function itself must read the store, not the order.
	fn := functionSource(t, "shipment_service.go", "returnDestinationFor")
	if !strings.Contains(fn, "ReturnAddress(ctx, s.stores.Pool(), sellerID)") {
		t.Errorf("the return destination does not come from the store's return address:\n%s", fn)
	}
	if strings.Contains(fn, "s.orders.ByID") {
		t.Errorf("the return destination is derived from the ORDER:\n%s\n"+
			"an order's only address is the buyer's", fn)
	}
}

// An unset return address must REFUSE the label, not guess one.
//
// The three available fallbacks are all wrong: reuse the buyer's address (goods go
// back to the buyer), invent one from the store's city (an address nobody chose), or
// silently succeed. Only the refusal is correct, and it has to be visible.
func TestAnUnsetReturnAddressRefusesRatherThanGuesses(t *testing.T) {
	fn := functionSource(t, "shipment_service.go", "returnDestinationFor")

	if !strings.Contains(fn, "return carrier.Address{}") {
		t.Errorf("an unreadable or unset return address does not yield a zero address:\n%s\\n"+
			"the carrier's Destination.IsZero() check is what turns this into a refusal, "+
			"and a partially-populated address passes IsZero for any single field", fn)
	}
	if !strings.Contains(fn, "if s.stores == nil") {
		t.Errorf("a nil store repository does not yield a zero address:\n%s", fn)
	}
	// And the store repository must treat a malformed address as absent, not as a
	// partially-filled one.
	raw, err := os.ReadFile("../repository/store_repo.go")
	if err != nil {
		t.Fatalf("read store_repo.go: %v", err)
	}
	read := functionSourceInRepository(t, string(raw), "ReturnAddress")
	if !strings.Contains(read, "return nil, nil") {
		t.Errorf("a malformed return address is not treated as absent:\n%s", read)
	}
}

// A return parcel must NOT touch shipped_quantity.
//
// shipped_quantity is the over-shipment counter, and DeriveShippingStatus compares it
// against `quantity` to decide whether an order is partially_shipped or shipped.
// Incrementing it for goods travelling the OTHER direction would mean a delivered
// order re-asserts `shipped`, and one unit of a line is counted twice -- once out and
// once back.
//
// This is the same defect 00050 fixed for the ORDER status arriving one level down.
func TestAReturnParcelNeverReservesShippedUnits(t *testing.T) {
	body := codeOnly(t, "../repository/return_parcel_repo.go")

	if strings.Contains(body, "shipped_quantity") {
		t.Errorf("the return path writes shipped_quantity:\n%s\\n"+
			"a return is not a new outbound shipment; counting it makes the order read "+
			"as more shipped than was bought, and re-asserts the order's status", body)
	}
	// It must NOT delegate to the outbound reservation either.
	if strings.Contains(body, "AddShipmentItems") {
		t.Errorf("the return path reuses AddShipmentItems:\n%s\\n"+
			"that method's whole job is the reservation, and reusing it for a return "+
			"means the return reserves units too", body)
	}
	// The write must be a plain insert.
	if !strings.Contains(body, "INSERT INTO shipment_items") {
		t.Errorf("the return path does not insert the returned line:\n%s", body)
	}
}

// Only an APPROVED return may be parcelled.
//
// A `requested` return is one the seller has not agreed to, and a parcel for it is
// goods in motion that nobody has accepted responsibility for.
func TestOnlyAnApprovedReturnMayBeParcelled(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "CreateReturnParcel")

	if !strings.Contains(body, "status != domain.ReturnApproved") {
		t.Errorf("an unapproved return can be parcelled:\n%s", body)
	}
	if !strings.Contains(body, "RETURN_NOT_APPROVED") {
		t.Errorf("an unapproved return is not reported by name:\n%s", body)
	}
}

// One parcel per return.
//
// Two boxes arriving against one approved refund means one of them matches nothing
// and the seller has to decide by eye.
func TestAReturnMayOnlyHaveOneParcel(t *testing.T) {
	body := functionSourceInRepository(t,
		readFileAt(t, filepathJoin("..", "repository", "return_parcel_repo.go")),
		"CreateReturnParcel")

	// The CONDITION, not the error value.
	//
	// This is the FIFTH time this session that a present-or-absent substring assertion
	// produced a false pass -- M38, M39, M45, M59, and now M62. Every one was the same
	// mistake: asserting that a string is PRESENT, which says nothing about whether the
	// code around it RUNS. `if false { return nil, ErrReturnAlreadyParcelled }` still
	// contains the error's name and refuses nothing.
	if !strings.Contains(body, "if err == nil {") {
		t.Errorf("the existing-parcel read is gone, so a second parcel is allowed:\n%s", body)
	}
	if !strings.Contains(body, "ErrReturnAlreadyParcelled") {
		t.Errorf("a second return parcel is not reported by name:\n%s", body)
	}
	// The read must come BEFORE the create, or two concurrent requests both see
	// "none yet".
	if strings.Index(body, "return_shipment_id IS NOT NULL") > strings.Index(body, "CreateShipment(ctx, q") {
		t.Errorf("the existing parcel is read AFTER the new one is created:\n%s\\n"+
			"two concurrent requests both read `none yet` and both create one", body)
	}
}

// The journey lives on the PARCEL, and the return row only follows it.
//
// A return marked returned with the box still in a depot is a refund paid for goods
// nobody has. The guard must therefore be the PARCEL's delivery, not the caller's
// word -- and a seller clicking "it arrived" is not evidence.
func TestAReturnIsMarkedReturnedOnlyWhenItsParcelIsDelivered(t *testing.T) {
	body := functionSourceInRepository(t,
		readFileAt(t, filepathJoin("..", "repository", "return_parcel_repo.go")),
		"MarkReturnArrived")

	if !strings.Contains(body, "s.status = 'delivered'") {
		t.Errorf("the return is not gated on the parcel being delivered:\n%s\\n"+
			"a return marked returned with the box in a depot is a refund paid for "+
			"goods nobody has", body)
	}
	if !strings.Contains(body, "s.delivered_at IS NOT NULL") {
		t.Errorf("the guard accepts a delivered flag that was never set:\n%s", body)
	}
	// And no new status value on the return row.
	if !strings.Contains(body, "r.status = 'approved'") {
		t.Errorf("the return transition is not guarded on `approved`:\n%s\n"+
			"without it a rejected or refunded return can be dragged back to returned", body)
	}

	// The row may ONLY be moved to 'returned', asserted EXACTLY.
	//
	// 'in_transit' is not even a legal value in the 00006 CHECK, so setting it would be
	// a constraint error on every arrival. But the mutation that introduced it
	// SURVIVED a test that checked the parcel gate and the `approved` guard and said
	// nothing about what the row is SET to. That was the gap: both guards can be
	// entirely correct and the destination still wrong.
	//
	// The journey lives on the PARCEL. A return row claiming `in_transit` duplicates it
	// in a second table, which is the disagreement 00050 had to fix for the order
	// status arriving one level down.
	if !strings.Contains(body, "SET status = 'returned'") {
		t.Errorf("the return row is not set to exactly 'returned':\n%s\n"+
			"the journey lives on the parcel; a second copy of it in this table is the "+
			"same class of defect as an order status disagreeing with its own boxes", body)
	}
	for _, illegal := range []string{"in_transit", "shipped", "delivered"} {
		if strings.Contains(body, "SET status = '"+illegal+"'") {
			t.Errorf("the return row can be set to %q:\n%s\n"+
				"that is a parcel status, not a return status", illegal, body)
		}
	}
}

// NOTHING ON THE RETURN PATH MAY MOVE MONEY.
//
// A return label is a label. The refund is `RefundReturn`, an explicit seller action,
// and the seller hold taken when the return was raised is released by the existing
// reservation job. If a carrier webhook that anyone can make say "delivered" could
// release escrow, that is a payout with no human in the loop.
//
// This is asserted over the whole file, the same way `payout_batch_test.go` asserts
// that no batch path writes `payouts.status`.
func TestNoReturnPathMovesMoney(t *testing.T) {
	for _, file := range []string{"shipment_service.go", "../repository/return_parcel_repo.go"} {
		// COMMENTS STRIPPED. Not tidiness: the section comment on this very file
		// explains that `AddShipmentItems` is deliberately not reused, and the service
		// comment explains that the refund is `RefundReturn`. Both statements are true
		// and both mention the forbidden symbol, so the unstripped version of this test
		// failed on its own documentation.
		//
		// "This file must not mention X" is not the property. "This file must not CALL
		// X" is. A file that explains why it avoids something is the good case, not
		// the bad one, and a check that punishes it teaches the next author to write
		// no comments at all.
		body := codeOnly(t, file)
		for _, forbidden := range []string{
			"ReleaseEscrow", "RefundOrder", "RefundReturn", "postLedger", "postLedgerStrict",
			"ReleaseExpiredReservations", "WalletHeldTxOn", "MarkReservationReleasedTx",
			"ReserveSellerPendingTx", "CreateRefund", "RecordProviderRefund",
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s references %s:\n"+
					"a return label is a label. The refund is an explicit seller action "+
					"and the hold is released by the reservation job. A carrier that can "+
					"be made to say `delivered` must not be able to pay a seller.",
					file, forbidden)
			}
		}
	}
}

// The BUYER creates the parcel; the SELLER issues the label.
//
// A seller who could create and dispatch a return parcel could mark goods as
// returned without them ever leaving the buyer's house, and the refund that follows is
// real money.
func TestTheBuyerCreatesTheParcelAndTheSellerIssuesTheLabel(t *testing.T) {
	raw, err := os.ReadFile("order_service.go")
	if err != nil {
		t.Fatalf("read order_service.go: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "func (s *OrderService) CreateReturnParcel") {
		t.Errorf("the buyer cannot create a return parcel:\n" +
			"the party that physically hands the parcel over must be the party that " +
			"records it")
	}
	if !strings.Contains(body, "assertReturnBuyer") {
		t.Errorf("the buyer-side path does not check the return belongs to the caller:\n%s", body)
	}
	// A 404 rather than a 403, so the endpoint cannot enumerate return ids.
	anon := functionSource(t, "order_service.go", "assertReturnBuyer")
	// The COMPARISON, not the sentinel. `if false { return domain.ErrNotFound }` still
	// contains the sentinel and authorises everyone -- which is the entire fraud
	// control on this feature, since a caller who can mark goods as returned without
	// them leaving the buyer gets a real refund.
	if !strings.Contains(anon, "if owner != buyerID {") {
		t.Errorf("the ownership comparison is gone, so any caller may create a return "+
			"parcel for any return:\n%s", anon)
	}
	if !strings.Contains(anon, "domain.ErrNotFound") {
		t.Errorf("someone else's return is a FORBIDDEN rather than a NOT_FOUND:\n%s\n"+
			"a 403 confirms the id exists, which turns this into an enumeration oracle", anon)
	}

	// The seller side issues the label, and does NOT create the parcel.
	seller, err := os.ReadFile("seller_service.go")
	if err != nil {
		t.Fatalf("read seller_service.go: %v", err)
	}
	sb := string(seller)
	if !strings.Contains(sb, "func (s *SellerService) BuyReturnLabel") {
		t.Error("the seller cannot issue a return label")
	}
	if strings.Contains(sb, "func (s *SellerService) CreateReturnParcel") {
		t.Error("the SELLER can create a return parcel:\n" +
			"that is the fraud this split exists to prevent -- goods marked as returned " +
			"without ever having left the buyer")
	}
}

// The routes must exist and land on the right parties.
func TestReturnRoutesExistForBothParties(t *testing.T) {
	src, err := os.ReadFile(filepathJoin("..", "httpapi", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	body := string(src)

	for _, want := range []string{
		`r.Put("/return-address", seller.SetReturnAddress)`,
		`r.Post("/returns/{id}/return-label", seller.BuyReturnLabel)`,
		`r.Post("/returns/{id}/arrived", seller.NoteReturnArrived)`,
		`ordersH.CreateReturnParcel`,
		`ordersH.ReturnParcel`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("router.go does not register %s", want)
		}
	}
	// The buyer routes must be OUTSIDE the seller group, or the ownership split is
	// only documented and not enforced by the wiring.
	start := strings.Index(body, `r.Route("/seller"`)
	sellerGroup := body[start:]
	if end := strings.Index(sellerGroup[1:], "\n\t\tr.Route("); end > 0 {
		sellerGroup = sellerGroup[:end]
	}
	if strings.Contains(sellerGroup, "ordersH.CreateReturnParcel") {
		t.Error("the buyer route for creating a return parcel is registered inside the " +
			"/seller group; the parcel must be created by the party handing it over")
	}
}

// methodSourceIn reads a method body out of `body` for a repository receiver.
func functionSourceInRepository(t *testing.T, body, fn string) string {
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

// codeOnly returns a file with its comments removed.
//
// String literals are left alone. That is a deliberate limitation and it is the right
// trade: a false POSITIVE (a forbidden word inside a string) is obvious and harmless,
// while a false NEGATIVE -- silently passing because a comment was stripped -- is the
// failure that matters. A regex-stripper that also handled strings could hide the very
// call it is looking for inside a string constant.
func codeOnly(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var b strings.Builder
	src := string(raw)
	for {
		// line comment
		i := strings.Index(src, "//")
		if i < 0 {
			b.WriteString(src)
			break
		}
		b.WriteString(src[:i])
		rest := src[i:]
		end := strings.Index(rest, "\n")
		if end < 0 {
			break
		}
		src = rest[end+1:]
	}
	return b.String()
}
