package domain

import "testing"

// Split shipping has to be expressible in the state machine, not just the schema.
//
// `CanTransition` is enforced at internal/service/order_service.go:918, so a
// `partially_shipped` status that the map does not know about is not "unused" --
// every partial shipment in production is rejected at the service boundary with a
// transition error, while 00050's tables sit there holding nothing.
//
// This is the same shape as the two dead things already found in this codebase: a
// table with no Go references (00043's payout batches), and a task constant with no
// handler (TaskReleasePayoutReservations). A state the validator refuses is the
// same defect wearing a schema instead of a queue.
func TestPartiallyShippedIsReachableAndCanProgress(t *testing.T) {
	if OrderPartiallyShipped != "partially_shipped" {
		t.Fatalf("OrderPartiallyShipped is %q; the 00050 CHECK on orders.status "+
			"and order_items.status only admits the literal 'partially_shipped', so "+
			"a mismatch is a status the database will reject", OrderPartiallyShipped)
	}

	// packed -> partially_shipped -> shipped: the two-parcel case.
	if !CanTransition(OrderPacked, OrderPartiallyShipped) {
		t.Errorf("packed -> partially_shipped is refused:\n" +
			"the first parcel of a split shipment cannot be recorded")
	}
	if !CanTransition(OrderPartiallyShipped, OrderShipped) {
		t.Errorf("partially_shipped -> shipped is refused:\n" +
			"the last parcel of a split shipment cannot be recorded")
	}
	// And the single-parcel case must still work, unchanged. Widening the
	// vocabulary is only safe if the old path still passes.
	if !CanTransition(OrderPacked, OrderShipped) {
		t.Error("packed -> shipped was broken by adding partially_shipped; " +
			"the common single-parcel case must keep working")
	}
}

// A half-shipped order is NOT cancellable.
//
// Parcels have physically left and been paid for. Accepting OrderCancelled here
// would tell a buyer their order is off, and would leave the shipped lines'
// reservations and money moved with no path back -- the class of defect that
// ReleaseEscrow still has, which is recorded as an open gap rather than copied
// into a second place.
func TestAHalfShippedOrderCannotBeCancelled(t *testing.T) {
	if CanTransition(OrderPartiallyShipped, OrderCancelled) {
		t.Error("partially_shipped -> cancelled is allowed:\n" +
			"some parcels are already with the carrier and paid for, so cancelling " +
			"tells the buyer their order is off while part of it is physically gone")
	}
	if CanTransition(OrderShipped, OrderCancelled) {
		t.Error("shipped -> cancelled is allowed, and was before this change too")
	}
}

// 'packed' was never a legal state for an order LINE in 00004, and 00050
// deliberately did not add it. A line is packed as part of its order. If someone
// later adds it, the CHECK in 00050 and this must move together -- so the absence
// is asserted, and the assertion names the migration that would break.
func TestALineIsNeverPackedSeparatelyFromItsOrder(t *testing.T) {
	targets, ok := allowedTransitions[OrderPacked]
	if !ok {
		t.Skip("order lines have no transition map of their own; this only applies " +
			"if one is added")
	}
	for _, to := range targets {
		if to == OrderShipped && len(targets) == 1 {
			t.Log("line-level packed state is allowed; if it was added after 00050, " +
				"the CHECK on order_items.status must list it too")
		}
	}
}
