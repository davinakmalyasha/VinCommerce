package repository

import (
	"os"
	"strings"
	"testing"
)

// Over-shipping is the defect the entire shipped_quantity column exists to prevent.
//
// The seller supplies these numbers, and nothing in the schema stops a parcel
// claiming four units of a line bought as three. The seller is then told to ship
// four and the buyer told four are coming, and the extra unit has no order behind
// it.
//
// The mechanism is the conditional update's PREDICATE, not the CHECK: the CHECK only
// fires when the item row is written, so two concurrent parcels could each pass it
// and jointly overshoot. Zero rows-affected is the refusal, decided atomically
// against the row lock.
func TestAParcelCannotClaimMoreUnitsThanWereBought(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "AddShipmentItems")

	if !strings.Contains(body, "shipped_quantity = shipped_quantity + $2") {
		t.Errorf("the reservation does not increment shipped_quantity:\n%s\n"+
			"without the counter there is nothing to compare against, so the "+
			"over-shipment check has no input", body)
	}
	if !strings.Contains(body, "AND shipped_quantity + $2 <= quantity") {
		t.Errorf("the reservation has no over-shipment predicate:\n%s\n"+
			"the CHECK on order_items only fires when the item row is written, so "+
			"two concurrent parcels would each pass it and jointly overshoot", body)
	}

	// The refusal must be READ, not inferred. A caller that checks `err != nil`
	// and continues has "overshipped" recorded as a soft failure.
	if !strings.Contains(body, "tag.RowsAffected() == 0") {
		t.Errorf("a failed reservation is not detected:\n%s\n"+
			"a zero-row UPDATE looks exactly like a success to a caller that only "+
			"checks the error", body)
	}
	if !strings.Contains(body, "ErrWouldOvership") {
		t.Errorf("a failed reservation does not refuse:\n%s\n"+
			"a parcel that ships more than was bought must be refused, not clamped "+
			"-- clamping would ship a different quantity than the parcel records", body)
	}
}

// The reservation must come BEFORE the shipment_items row.
//
// Reserving after inserting leaves a row claiming units that were never reserved,
// and the two tables then disagree -- with nothing to reconcile them, because the
// CHECK that would notice only covers order_items.
func TestTheLineIsReservedBeforeTheParcelRowIsWritten(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "AddShipmentItems")

	reserve := strings.Index(body, "UPDATE order_items")
	insert := strings.Index(body, "INSERT INTO shipment_items")
	if reserve < 0 || insert < 0 {
		t.Fatalf("AddShipmentItems is missing one of the two writes:\n%s", body)
	}
	if reserve > insert {
		t.Errorf("the shipment_items row is written before the line is reserved:\n%s\n"+
			"a row claiming units that were never reserved, with the two tables "+
			"disagreeing and no reconciliation between them", body)
	}
}

// A parcel with nothing in it is not a parcel.
func TestAParcelWithNoLinesIsRefused(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "AddShipmentItems")
	if !strings.Contains(body, "SHIPMENT_EMPTY") {
		t.Errorf("an empty parcel is accepted:\n%s\n"+
			"a zero-weight parcel with a tracking number is how a phantom shipment "+
			"reaches a buyer's order page", body)
	}
	// And a repeated line must be refused rather than relying on the PRIMARY KEY
	// to reject the second row, which surfaces as a raw constraint error.
	if !strings.Contains(body, "SHIPMENT_ITEM_DUPLICATE") {
		t.Errorf("a repeated line in one parcel reaches the primary key:\n%s\n"+
			"the seller meant '4 of this line', typed as two lines, and the error "+
			"they see reads like a bug", body)
	}
}

// Weight is derived from the contents and never accepted from a seller.
//
// A weight the seller types is a weight the carrier charges for. If the recorded
// weight disagrees with what is in the box, the parcel is billed wrong and any
// surcharge lands on the buyer for a reason nobody can reconstruct.
func TestShipmentWeightIsDerivedNotAccepted(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "RefreshShipmentWeight")
	if !strings.Contains(body, "SUM(oi.weight_grams * si.quantity)") {
		t.Errorf("the weight is not summed from the parcel's lines:\n%s\n"+
			"weight_grams must be a function of what is actually in the box", body)
	}

	raw, err := os.ReadFile("shipment_repo.go")
	if err != nil {
		t.Fatalf("read shipment_repo.go: %v", err)
	}
	all := string(raw)
	// No INSERT may name weight_grams, or a caller could set a weight the contents
	// do not support.
	for _, stmt := range statementsContaining(all, "INSERT INTO shipments") {
		if strings.Contains(stmt, "weight_grams") {
			t.Errorf("a shipment INSERT sets weight_grams, so a weight can be "+
				"supplied instead of derived:\n%s", stmt)
		}
	}
}

// 'exception' must not be a dead end.
//
// A carrier that reports a delivery problem usually re-delivers. If the later
// successful delivery were refused, a recoverable hiccup would leave a parcel stuck
// in a failure state for ever, which is worse than the hiccup.
func TestADeliveryExceptionCanStillBecomeDelivered(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "MarkShipmentDelivered")
	if !strings.Contains(body, "'exception'") {
		t.Errorf("a parcel in 'exception' cannot still be delivered:\n%s\n"+
			"a carrier that reports a problem usually re-delivers, and refusing "+
			"that strands the parcel in a failure state permanently", body)
	}
	if !strings.Contains(body, "delivered_at IS NULL") {
		t.Errorf("a repeated delivery callback is not a no-op:\n%s", body)
	}
}

// 'ready' -> 'shipped' is legal only for a manual carrier.
//
// A parcel with no label that goes shipped has skipped the step that produces the
// tracking number the buyer is shown. Manual carriers genuinely have no AWB, so
// the case is allowed for them -- and distinguished, rather than one of the two
// cases being quietly dropped.
func TestAParcelWithoutALabelIsShippableOnlyWhenTheCarrierIsManual(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "MarkShipmentShipped")
	if !strings.Contains(body, "status = 'label_created'") {
		t.Errorf("a parcel can be handed over without a label:\n%s\n"+
			"that skips the step producing the tracking number the buyer is shown", body)
	}
	if !strings.Contains(body, "carrier = 'manual'") {
		t.Errorf("the no-label path is not restricted to manual carriers:\n%s\n"+
			"every carrier can then take a parcel with no AWB", body)
	}
	// And once delivered, no transition may move it back.
	if !strings.Contains(body, "AND delivered_at IS NULL") {
		t.Errorf("a delivered parcel can be moved back to in_transit:\n%s", body)
	}
}

// The parcel number is a fact about the order, not a request.
//
// Two parcels both claiming to be the first either fail the UNIQUE -- which is safe
// but a confusing error for a seller who double-tapped -- or get renumbered by a
// retry, leaving a hole in the audit trail.
func TestParcelNumberingComesFromTheOrder(t *testing.T) {
	body := functionSource(t, "shipment_repo.go", "CreateShipment")
	if !strings.Contains(body, "MAX(sequence)") {
		t.Errorf("the parcel number is not derived from the order's existing parcels:\n%s", body)
	}

	// The signature is the assertion. `$3` appears in the INSERT, but it carries
	// the derived value, so its mere presence proves nothing -- the previous
	// version of this test checked for `$3` and failed on correct code for exactly
	// that reason. What must not exist is a sequence the CALLER chooses.
	sig := body[:strings.Index(body, "{")]
	for _, forbidden := range []string{"sequence int", "seq int", "sequence,"} {
		if strings.Contains(sig, forbidden) {
			t.Errorf("CreateShipment accepts a sequence from the caller (%q):\n%s\n"+
				"the parcel number is a fact about the order; a caller that picks it "+
				"can collide with an existing parcel, which surfaces as a UNIQUE "+
				"violation on a double-tap, or leaves a hole after a renumbering",
				forbidden, sig)
		}
	}
	// And the value bound to the INSERT must be the derived one.
	if !strings.Contains(body, "nextSeq, kind, carrier") {
		t.Errorf("the INSERT is not fed the derived number:\n%s", body)
	}
}

// helpers shared by the assertions above

// functionSource returns the body of the named method in `file`.
func functionSource(t *testing.T, file, method string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := string(raw)
	i := strings.Index(body, "func (r *ShipmentRepository) "+method+"(")
	if i < 0 {
		t.Fatalf("%s: method %s not found", file, method)
	}
	rest := body[i:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		rest = rest[:end]
	}
	return rest
}

// statementsContaining returns each backtick-delimited statement mentioning needle.
func statementsContaining(body, needle string) []string {
	out := []string{}
	rest := body
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			return out
		}
		start := strings.LastIndex(rest[:i], "`")
		end := strings.Index(rest[i:], "`")
		if start < 0 || end < 0 {
			return out
		}
		out = append(out, rest[start+1:i+end])
		rest = rest[i+end:]
	}
}
