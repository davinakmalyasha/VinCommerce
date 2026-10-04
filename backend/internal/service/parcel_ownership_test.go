package service

import (
	"strings"
	"testing"
)

// The parcel and return endpoints added with F1/F4a took the id straight from the URL
// and passed it to the repository. Every one of them was therefore readable, spendable,
// or mutable by ANY authenticated seller who guessed or enumerated an id:
//
//	GET  /seller/orders/{id}/parcels    discloses the order's carrier, tracking numbers
//	                                       and the seller's own stock movements
//	POST /seller/returns/{id}/return-label  spends real money at a real carrier, on a
//	                                       return belonging to another store, addressed
//	                                       to that store
//	GET  /seller/returns/{id}/parcel    discloses the same, plus the pickup address
//	POST /seller/returns/{id}/arrived   moves the gate on RefundReturn, so it is a
//	                                       money bug wearing an authorisation bug's
//	                                       clothes
//
// These assert each one checks. There is no database here, so they are source
// assertions, as the rest of this package's ownership tests are.

// Every seller-facing parcel/return endpoint must take an actor and compare it to the
// owner. A method that accepts only an id is the shape of the defect.
func TestParcelAndReturnEndpointsAreSellerScoped(t *testing.T) {
	cases := []struct {
		fn        string
		wantActor string
	}{
		{"OrderParcels", "actorID"},
		{"ReturnParcelFor", "actorID"},
		{"BuyReturnLabel", "actorID"},
		{"NoteReturnArrived", "actorID"},
	}

	src := readSource(t, "shipment_service.go")
	for _, c := range cases {
		body := funcBody(t, src, "func (s *ShipmentService) "+c.fn+"(")
		if !strings.Contains(body, c.wantActor+" string") &&
			!strings.Contains(body, c.wantActor+",") {
			t.Errorf("ShipmentService.%s does not accept an actor.\n%s\n"+
				"Every seller-facing parcel endpoint needs the caller, or it is an IDOR "+
				"for any authenticated seller who can guess an id", c.fn, firstLines(body, 6))
		}
		if !strings.Contains(body, "assertReturnSeller(") && !strings.Contains(body, "p.SellerID != actorID") {
			t.Errorf("ShipmentService.%s accepts an actor but never compares it to the owner.\n%s\n"+
				"Taking the id and ignoring the caller is the same defect with one more "+
				"argument attached", c.fn, firstLines(body, 14))
		}
	}
}

// The ownership comparison has to happen BEFORE the carrier is called, not after.
// Checking after `c.BuyLabel` would still have spent the money.
func TestReturnLabelOwnershipIsCheckedBeforeTheCarrierIsCharged(t *testing.T) {
	body := funcBody(t, readSource(t, "shipment_service.go"),
		"func (s *ShipmentService) BuyReturnLabel(")

	guardAt := strings.Index(body, "assertReturnSeller(")
	carrierAt := strings.Index(body, ".BuyLabel(")

	if guardAt < 0 {
		t.Fatal("BuyReturnLabel does not check ownership at all")
	}
	if carrierAt < 0 {
		t.Fatal("BuyReturnLabel no longer calls the carrier; update this test")
	}
	if guardAt > carrierAt {
		t.Errorf("the ownership check is at offset %d but the carrier is charged at %d.\n%s\n"+
			"A check placed after the label is bought does not prevent the spend -- the "+
			"money is gone at the carrier by then, whether or not the request 403s",
			guardAt, carrierAt, body)
	}
}

// OrderParcels cannot answer "is this actor the seller" from the order id: one checkout
// can produce several orders, and a split order mixes stores. The rule is that EVERY
// parcel must belong to the actor -- "any parcel matches" would disclose the other
// store's tracking numbers.
func TestOrderParcelsRequiresEveryParcelToBelongToTheActor(t *testing.T) {
	body := funcBody(t, readSource(t, "shipment_service.go"),
		"func (s *ShipmentService) OrderParcels(")

	loopAt := strings.Index(body, "for _, p := range parcels")
	if loopAt < 0 {
		t.Fatal("OrderParcels does not iterate the parcels; update this test")
	}
	guard := body[loopAt:]
	if end := strings.Index(guard, "\n\t}"); end > 0 {
		guard = guard[:end]
	}
	if !strings.Contains(guard, "if p.SellerID != actorID {") {
		t.Errorf("the per-parcel check does not compare the parcel's seller to the actor:\n%s\n"+
			"Without this the actor only has to own ONE parcel of a split order to read "+
			"the other store's. As with assertReturnSeller the brace is load-bearing: "+
			"`if false && p.SellerID != actorID` passes a bare-substring check while "+
			"refusing nobody", guard)
	}
	if strings.Contains(guard, "break") {
		t.Errorf("the loop breaks instead of refusing:\n%s\n"+
			"Breaking on the first mismatch still returns the full parcel list", guard)
	}
}

// assertReturnSeller is the shared guard, so it must actually compare and must refuse.
func TestAssertReturnSellerRefusesAndCompares(t *testing.T) {
	src := readSource(t, "shipment_service.go")
	body := funcBody(t, src, "func (s *ShipmentService) assertReturnSeller(")

	if !strings.Contains(body, "returnFacts(") {
		t.Error("assertReturnSeller does not load the return's seller.\n" +
			"It cannot compare what it never read, so this would be a function that " +
			"returns nil and authorises everyone")
	}
	if !strings.Contains(body, "if sellerID != actorID {") {
		t.Error("assertReturnSeller never compares sellerID to actorID.\n" +
			"NOTE the brace: asserting the bare substring `sellerID != actorID` is NOT " +
			"enough, and this test was wrong about that until it was mutation-checked. " +
			"`if false && sellerID != actorID {` leaves that substring intact, so the " +
			"substring test passed on a guard that never refuses anyone. Requiring the " +
			"closing brace forces the comparison to BE the condition.")
	}
	if !strings.Contains(body, "KindForbidden") || !strings.Contains(body, "NOT_OWNED") {
		t.Error("assertReturnSeller does not refuse.\n" +
			"It must return a Forbidden/NOT_OWNED error; BuyLabel already uses that pair, " +
			"and a guard that cannot fail is not a guard")
	}
	// The empty-actor escape hatch is the one way this function can be bypassed, so it
	// has to be documented. The documentation lives in the doc comment ABOVE the
	// function, which is where every other comment in this package lives -- so read
	// backwards from the declaration rather than forwards from it.
	decl := strings.Index(src, "func (s *ShipmentService) assertReturnSeller(")
	if decl < 0 {
		t.Fatal("no assertReturnSeller")
	}
	doc := src[:decl]
	docAt := strings.LastIndex(doc, "no seller context")
	if docAt < 0 {
		docAt = strings.LastIndex(doc, `actorID == ""`)
	}
	docWindow := ""
	if docAt >= 0 {
		lo, hi := docAt-200, docAt+400
		if lo < 0 {
			lo = 0
		}
		if hi > len(doc) {
			hi = len(doc)
		}
		docWindow = doc[lo:hi]
	}
	if strings.Contains(body, `if actorID == ""`) {
		if !strings.Contains(docWindow, "not reachable from a request") &&
			!strings.Contains(docWindow, "no seller context") {
			t.Error("assertReturnSeller returns nil for an empty actor, and neither the code " +
				"nor its doc comment says that no HTTP path can produce one.\nThat is a bypass " +
				"someone will rely on the first time a handler forgets to pass the user")
		}
	}
}

// The handlers must pass the AUTHENTICATED user. Passing a literal, or an id taken from
// the request body or a query parameter, is the same IDOR with extra steps.
func TestParcelHandlersPassTheAuthenticatedUserNotAnIDFromTheURL(t *testing.T) {
	src := readSource(t, "../httpapi/handler/seller.go")
	for _, fn := range []string{"OrderParcels", "ReturnParcels", "BuyReturnLabel", "NoteReturnArrived"} {
		body := funcBody(t, src, "func (h *Seller) "+fn+"(")
		if !strings.Contains(body, "middleware.UserFrom(") {
			t.Errorf("Seller.%s does not read the authenticated user.\n%s\n"+
				"It must pass that identity through to the service so ownership can be "+
				"checked; the {id} path parameter identifies the ROW, never the CALLER",
				fn, firstLines(body, 8))
		}
		if strings.Contains(body, `chi.URLParam(r, "id"), nil`) {
			t.Errorf("Seller.%s passes an empty actor alongside the id", fn)
		}
	}
}

// Every one of those routes lives inside the seller group, which is authenticated and
// role-gated. The `actorID == ""` branch in assertReturnSeller is therefore unreachable
// from HTTP, which is the only thing making it safe.
func TestParcelRoutesAreAuthenticatedAndRoleGated(t *testing.T) {
	router := readFileAt(t, "../httpapi/router.go")

	start := strings.Index(router, `r.Route("/seller"`)
	if start < 0 {
		t.Fatal("no /seller route group")
	}
	group := router[start:]
	if end := strings.Index(group[1:], "\n\tr.Route("); end > 0 {
		group = group[:end]
	}

	if !strings.Contains(group, "r.Use(authMw)") {
		t.Fatal("/seller is not behind authMw; the actor could be nil in every handler")
	}
	if !strings.Contains(group, "RequireRoles(") {
		t.Error("/seller is not role-gated")
	}

	for _, route := range []string{
		`r.Get("/orders/{id}/parcels"`,
		`r.Get("/returns/{id}/parcel"`,
		`r.Post("/returns/{id}/return-label"`,
		`r.Post("/returns/{id}/arrived"`,
	} {
		if !strings.Contains(group, route) {
			t.Errorf("route %s is not registered in the authenticated seller group.\n"+
				"If it moved out of the group, the actor may be empty and the "+
				"ownership checks are bypassed by design", route)
		}
	}
}

// The buyer-side read is guarded at the handler too. This is not a fix -- it was already
// there -- but the service check added alongside it is defence in depth, and a test that
// only asserted one of the two would let the other be deleted silently.
func TestBuyerReturnParcelIsCheckedInBothHandlerAndService(t *testing.T) {
	h := funcBody(t, readSource(t, "../httpapi/handler/cart.go"),
		"func (h *Orders) ReturnParcel(")
	if !strings.Contains(h, "AssertReturnOwner(") {
		t.Errorf("Orders.ReturnParcel does not check the caller:\n%s", firstLines(h, 14))
	}

	s := funcBody(t, readSource(t, "order_service.go"),
		"func (s *OrderService) ReturnParcelFor(")
	if !strings.Contains(s, "assertReturnBuyer(") {
		t.Errorf("OrderService.ReturnParcelFor does not check the caller in the service.\n%s\n"+
			"The handler check covers today's only caller. The service check is what stops "+
			"the next one from inheriting an unguarded read of another buyer's parcel", s)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
