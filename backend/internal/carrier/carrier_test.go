package carrier

import (
	"context"
	"strings"
	"testing"
)

// Buying a label spends REAL MONEY at a real carrier. A retry after a timeout that
// produced a second label would leave the seller with two labels for one box and no
// way to tell which is live -- and the first one is already paid for.
func TestBuyingALabelIsIdempotentPerParcel(t *testing.T) {
	m := NewManual()
	in := goodInput()

	first, err := m.BuyLabel(context.Background(), in)
	if err != nil {
		t.Fatalf("first BuyLabel: %v", err)
	}
	second, err := m.BuyLabel(context.Background(), in)
	if err != nil {
		t.Fatalf("second BuyLabel: %v", err)
	}

	if first.Tracking != second.Tracking {
		t.Errorf("a retry minted a second tracking number:\n  first  %s\n  second %s\n"+
			"two labels for one box, both paid for, and the parcel's UNIQUE "+
			"(carrier, tracking_number) index cannot tell which is live",
			first.Tracking, second.Tracking)
	}
	if first.CarrierRef != second.CarrierRef {
		t.Errorf("a retry changed the carrier reference: %s then %s",
			first.CarrierRef, second.CarrierRef)
	}
}

// A different parcel must get a different number, or two boxes of the same goods
// become indistinguishable to anyone reading the tracking off the label.
func TestDifferentParcelsGetDifferentTrackingNumbers(t *testing.T) {
	m := NewManual()
	seen := map[string]string{}
	for _, id := range []string{"p-1", "p-2", "p-3", "p-4"} {
		in := goodInput()
		in.ParcelID = id
		lab, err := m.BuyLabel(context.Background(), in)
		if err != nil {
			t.Fatalf("BuyLabel(%s): %v", id, err)
		}
		if prev, dup := seen[lab.Tracking]; dup {
			t.Errorf("parcels %s and %s share tracking %s", prev, id, lab.Tracking)
		}
		seen[lab.Tracking] = id
	}
}

// The number must be visibly platform-minted.
//
// A seller or a support agent glancing at a tracking number must be able to tell that
// no courier is involved. Otherwise someone waits on a courier for a parcel that was
// never handed to one, and the platform has no way to explain why nothing updates.
func TestAManualTrackingNumberSaysItIsManual(t *testing.T) {
	m := NewManual()
	lab, err := m.BuyLabel(context.Background(), goodInput())
	if err != nil {
		t.Fatalf("BuyLabel: %v", err)
	}
	if !strings.HasPrefix(lab.Tracking, "MNL") {
		t.Errorf("tracking %q does not identify itself as platform-minted:\n"+
			"a number that looks like a courier's makes someone wait on a courier "+
			"for a parcel that was never handed to one", lab.Tracking)
	}
	if !strings.HasPrefix(lab.CarrierRef, "MAN-") {
		t.Errorf("carrier ref %q does not identify itself as manual", lab.CarrierRef)
	}
}

// The manual carrier must not invent movement it cannot know about.
//
// A reconciliation path that stored a guessed "in_transit" would be storing a lie as
// fact, and the parcel would sit in the system claiming to be moving for ever.
func TestTheManualCarrierNeverClaimsMovementItCannotKnow(t *testing.T) {
	m := NewManual()
	lab, err := m.BuyLabel(context.Background(), goodInput())
	if err != nil {
		t.Fatalf("BuyLabel: %v", err)
	}
	tr, err := m.Track(context.Background(), lab.Tracking)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if tr.Status == TrackingInTransit || tr.Status == TrackingDelivered {
		t.Errorf("the manual carrier reported %q for a parcel nobody is watching:\n"+
			"nothing is watching. A parcel handed over by hand reaches the buyer and "+
			"the platform is TOLD, not polled", tr.Status)
	}
	if tr.Status != TrackingPending {
		t.Errorf("the manual carrier reported %q; it is entitled to say only that a "+
			"label exists", tr.Status)
	}
	// And it must be able to explain itself, because "pending" with no detail is
	// indistinguishable from a stuck parcel.
	if strings.TrimSpace(tr.Detail) == "" {
		t.Error("the manual carrier reports a status with no explanation")
	}
}

// An unknown reference is ABSENT, not "unknown".
//
// That distinction is the one RefundStatusProvider draws for gateways: "this carrier
// does not know about that parcel" and "this carrier cannot tell you where it is" are
// different, and collapsing them makes every parcel look permanently stuck.
func TestAnUnknownTrackingIsAbsentRatherThanUnknown(t *testing.T) {
	m := NewManual()
	if _, err := m.Track(context.Background(), "MNLNOSUCH0001"); err == nil {
		t.Error("tracking a label this carrier never issued succeeded; a carrier must " +
			"be able to say a parcel is not its own")
	}
}

// A label that could not possibly be valid must be refused at the boundary.
//
// Checked in one place rather than per adapter: three carriers re-implementing "a
// label needs a destination" is three chances to forget one, and the failure at the
// far end is a paid label addressed to nowhere.
func TestALabelRequestIsRefusedBeforeAnyMoneyIsSpent(t *testing.T) {
	m := NewManual()
	cases := []struct {
		name   string
		mutate func(*BuyLabelInput)
	}{
		{"no parcel", func(i *BuyLabelInput) { i.ParcelID = "" }},
		{"no weight", func(i *BuyLabelInput) { i.WeightGrams = 0 }},
		{"negative weight", func(i *BuyLabelInput) { i.WeightGrams = -5 }},
		{"no destination", func(i *BuyLabelInput) { i.Destination = Address{} }},
		{"no format", func(i *BuyLabelInput) { i.Format = "" }},
	}
	for _, tc := range cases {
		in := goodInput()
		tc.mutate(&in)
		if _, err := m.BuyLabel(context.Background(), in); err == nil {
			t.Errorf("a label with %s was accepted:\n"+
				"a label is money spent; the refusals are the cheap part", tc.name)
		}
	}
}

// An unknown carrier must be named, not silently answered with the manual one.
//
// This is the failure mode a `switch` default would have introduced: a seller asking
// for "jnE" and silently receiving a self-minted tracking number, believing a courier
// is now involved. Being told the carrier is not configured is the useful answer.
func TestAnUnconfiguredCarrierIsNamed(t *testing.T) {
	r := NewLabelRegistry(NewManual())

	if _, err := r.Get("jne"); err == nil {
		t.Fatal("an unconfigured carrier resolved")
	} else if !strings.Contains(err.Error(), "jne") {
		t.Errorf("the error does not name the carrier that was asked for: %v", err)
	}

	// An empty name is the COD case and MUST resolve to manual.
	c, err := r.Get("")
	if err != nil {
		t.Errorf("an empty carrier name did not resolve to manual: %v", err)
	} else if c.Name() != ManualCarrierName {
		t.Errorf("an empty carrier name resolved to %q, not manual", c.Name())
	}
}

// `exception` is recoverable and must not be terminal.
//
// A missed delivery usually re-delivers. Mapping `attempted` to a failure would make
// a parcel permanently stuck, because `MarkShipmentDelivered` -- correctly -- only
// accepts delivered_at IS NULL and a non-failed status.
func TestARecoverableCarrierStateIsNotAFailure(t *testing.T) {
	for _, s := range []string{TrackingAttempted, TrackingReturned} {
		got, ok := MapTrackingStatus(s)
		if !ok {
			t.Errorf("%q has no shipment equivalent at all", s)
			continue
		}
		if got == "cancelled" || got == "voided" {
			t.Errorf("%q maps to %q:\n"+
				"a missed delivery usually re-delivers, and a parcel in a terminal "+
				"state can never be marked delivered afterwards", s, got)
		}
	}
	if got, _ := MapTrackingStatus(TrackingAttempted); got != "exception" {
		t.Errorf("attempted maps to %q, not exception", got)
	}
	if got, _ := MapTrackingStatus(TrackingDelivered); got != "delivered" {
		t.Errorf("delivered maps to %q", got)
	}
	if _, ok := MapTrackingStatus("something the carrier invented"); ok {
		t.Error("an unrecognised carrier state was translated; an unknown state must " +
			"leave the parcel alone rather than guess")
	}
}

func goodInput() BuyLabelInput {
	return BuyLabelInput{
		ParcelID:    "parcel-abc",
		OrderID:     "order-1",
		OrderNumber: "VC-0001",
		Carrier:     ManualCarrierName,
		Format:      "pdf",
		WeightGrams: 1500,
		Origin:      Address{Name: "Seller", Street: "Jl. A", City: "Jakarta", Postcode: "10101"},
		Destination: Address{Name: "Buyer", Street: "Jl. B", City: "Bandung", Postcode: "40151"},
		Idempotency: "parcel-abc",
	}
}
