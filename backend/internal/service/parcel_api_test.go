package service

import (
	"os"
	"strings"
	"testing"
)

// Buying a label SPENDS MONEY and is often irreversible at the carrier. Handing the
// box over is free and happens later. So they are separate endpoints.
//
// Collapsing them means a seller who buys a label and then discovers he cannot get a
// colleague to the depot has spent money he cannot get back, and the platform has no
// record of having done it.
func TestBuyingALabelAndDispatchingAreSeparate(t *testing.T) {
	src, err := os.ReadFile(filepathJoin("..", "httpapi", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	body := string(src)

	for _, want := range []string{
		`r.Post("/orders/{id}/parcels", seller.CreateParcel)`,
		`r.Get("/orders/{id}/parcels", seller.OrderParcels)`,
		`r.Post("/parcels/{id}/dispatch", seller.DispatchParcel)`,
		`r.Post("/parcels/{id}/label", seller.BuyParcelLabel)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("router.go does not register %s", want)
		}
	}

	// Inside the seller guard, not merely present somewhere in the file.
	start := strings.Index(body, `r.Route("/seller"`)
	if start < 0 {
		t.Fatal("no /seller group in router.go")
	}
	sellerGroup := body[start:]
	if end := strings.Index(sellerGroup[1:], "\n\t\tr.Route("); end > 0 {
		sellerGroup = sellerGroup[:end]
	}
	if !strings.Contains(sellerGroup, "/parcels") {
		t.Errorf("the parcel routes are not inside the /seller group:\n%s", sellerGroup)
	}
}

// The single-parcel shortcut must still exist, for the orders that were never split.
//
// A parcel API that replaces it entirely would be a regression: most orders are one
// box, and forcing a seller to name contents for an order going in a single parcel is
// more work for no information.
func TestTheSingleParcelShortcutStillWorks(t *testing.T) {
	src, err := os.ReadFile(filepathJoin("..", "httpapi", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	if !strings.Contains(string(src), `r.Post("/orders/{id}/transition", seller.FulfillOrder)`) {
		t.Error("the single-parcel transition route is gone; most orders are one box " +
			"and must not have to name their contents")
	}
}

// A parcel's contents are the server's to work out.
//
// The request names line ids and quantities, and the shipped_quantity counter is
// maintained server-side. A request that could set `contents` freely is a request that
// can put units in a box that were never bought.
func TestTheParcelRequestCannotDeclareContents(t *testing.T) {
	body := functionSource(t, "../httpapi/handler/seller.go", "CreateParcel")

	for _, forbidden := range []string{"ShipmentItems", "SetContents", "Contents:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the parcel request can declare contents (%q):\n%s\n"+
				"shipped_quantity is the thing that must be maintained by the server, "+
				"not trusted to the request", forbidden, body)
		}
	}
	// But it DOES accept lines, and refuses an empty parcel by name.
	if !strings.Contains(body, "OrderItemID") {
		t.Errorf("the parcel request does not carry line ids:\n%s", body)
	}
	if !strings.Contains(body, "SHIPMENT_EMPTY") {
		t.Errorf("an empty parcel is accepted:\n%s\n"+
			"a parcel with nothing in it has no weight, no contents and no reason to "+
			"exist, and it would look like a real parcel in every list", body)
	}
}

// An unknown carrier must be NAMED, never silently answered with the manual one.
//
// A seller asking for "jnE" and receiving a self-minted tracking number believes a
// courier is involved, and then spends three days waiting on one. This is why the
// registry is a map and not a switch with a default branch.
func TestAnUnknownCarrierIsNeverSilentlyReplacedByManual(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "BuyLabel")

	if !strings.Contains(body, "s.carriers.Get(shipment.Carrier)") {
		t.Errorf("the carrier is not resolved through the registry:\n%s", body)
	}
	if !strings.Contains(body, "CARRIER_NOT_CONFIGURED") {
		t.Errorf("an unconfigured carrier is not reported by name:\n%s", body)
	}
	// A delivered parcel must not get a new label: money spent on a box that is not
	// going anywhere.
	//
	// The CONDITION, not the error code. This is the FOURTH time in this session that
	// asserting a substring found inside a disabled branch produced a false pass --
	// M38, M39, M45, and now M59. The pattern is worth stating once: a string that is
	// PRESENT says nothing about whether the code around it RUNS.
	if !strings.Contains(body, "if shipment.DeliveredAt != nil {") {
		t.Errorf("the delivered-parcel guard is not reached, so money is spent on a "+
			"label for goods that are already at the buyer:\n%s", body)
	}
	if !strings.Contains(body, "SHIPMENT_ALREADY_DELIVERED") {
		t.Errorf("an already-delivered parcel is not reported by name:\n%s", body)
	}
	// And an empty parcel cannot be labelled.
	if !strings.Contains(body, "len(contents) == 0") {
		t.Errorf("an empty parcel can be labelled:\n%s", body)
	}
}

// The label idempotency key is the PARCEL, not the attempt.
//
// Buying a label spends real money; a retry after a timeout must return the label
// already paid for rather than buy a second one for the same box.
func TestTheLabelIdempotencyKeyIsTheParcel(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "BuyLabel")
	// Matched on the VALUE, not on gofmt's column alignment. An earlier version
	// asserted the literal "Idempotency:  shipment.ID" with two spaces and failed on
	// correct code purely because gofmt aligned the struct literal to one.
	i := strings.Index(body, "Idempotency:")
	if i < 0 {
		t.Fatalf("BuyLabel sets no idempotency key at all:\n%s\n"+
			"without one a retry after a timeout buys a second label for one box, and "+
			"the first was already paid for", body)
	}
	line := strings.TrimSpace(body[i:])
	if end := strings.Index(line, "\n"); end > 0 {
		line = strings.TrimSpace(line[:end])
	}
	// EXACTLY the parcel id. The first version of this check was
	//
	//     strings.Contains(line, "shipment.ID")
	//
	// and M58 mutated the value to `shipment.ID + "-retry"`, which still contains
	// the substring, so the mutation passed a test written to catch it. A key that
	// must be a single value has to be compared as one.
	if line != "Idempotency: shipment.ID," {
		t.Errorf("the label idempotency key is %q, want exactly the parcel id:\n"+
			"a key that varies per attempt turns a retry after a timeout into a "+
			"second PAID label for one box", line)
	}
}

// A carrier that cannot track says so.
//
// The alternative -- reporting every parcel as `unknown` -- is a parcel stuck in the
// system for ever with no indication of why, which is the same shape of defect as a
// reconciliation job that silently does nothing.
func TestACarrierThatCannotTrackIsNamedNotSilentlyUnknown(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "TrackParcel")

	if !strings.Contains(body, "carrier.TrackingCarrier") {
		t.Errorf("the tracking capability is not checked:\n%s", body)
	}
	if !strings.Contains(body, "CARRIER_CANNOT_TRACK") {
		t.Errorf("a carrier that cannot track is not reported by name:\n%s", body)
	}
	// And a parcel with nothing to ask about says THAT, rather than asking anyway.
	if !strings.Contains(body, "SHIPMENT_NOT_TRACKABLE") {
		t.Errorf("a parcel with no tracking reference is not refused by name:\n%s", body)
	}
}

// An empty shipping address must produce a refusal, not a fabricated destination.
//
// A label addressed to a guess is money spent and a parcel nobody receives. The
// carrier package refuses a zero address; this asserts the order's real address is
// what gets passed.
func TestTheDestinationIsTheOrdersRealAddress(t *testing.T) {
	body := functionSource(t, "shipment_service.go", "destinationFor")

	if !strings.Contains(body, "order.ShippingAddress") {
		t.Errorf("the destination is not the order's shipping address:\n%s", body)
	}
	// And an unreadable address yields the ZERO address, not a partial guess, so the
	// carrier's IsZero check can see it.
	if !strings.Contains(body, "return carrier.Address{}") {
		t.Errorf("an unreadable address does not yield a zero address:\n%s\n"+
			"a partially-populated address passes IsZero for any single field, and the "+
			"label goes somewhere invented", body)
	}
}

func BrokenOnPurpose(t *testing.T) { t.Fatal("deliberate") }
