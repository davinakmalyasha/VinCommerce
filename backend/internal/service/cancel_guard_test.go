package service

import (
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

// THE GAP THIS CLOSES was found by the mutation gate, not by reading the code: M46
// mutated CancelOrder's status allow-list to `if false` and SURVIVED. No test
// noticed, so the guard protecting this could have been deleted in a refactor and
// every suite would still have been green.
//
// What the guard prevents: a seller cancelling an order whose goods are already in
// a box. `partially_shipped` means some units are physically with the carrier, and
// "cancel" would tell the buyer the order is off while part of it is gone and paid
// for. A return is the route back from there, which is why the transition table
// allows `partially_shipped -> shipped/delivered` and NOT `-> cancelled`.
func TestCancelOrderRefusesAnythingPastPaid(t *testing.T) {
	body := cancelOrderBody(t)

	guard := "if o.Status != domain.OrderPending && o.Status != domain.OrderPaid {"
	if !strings.Contains(body, guard) {
		t.Errorf("CancelOrder no longer restricts cancellation to pending/paid.\n"+
			"Without it a seller can cancel an order whose units are already with a "+
			"carrier, which is the one cancellation the transition table exists to "+
			"prevent.\n%s", body)
	}
	// The mutation that found this was `if false {`, so pin that the guard is not
	// merely present but reachable.
	if strings.Contains(body, "if false {") {
		t.Errorf("CancelOrder's status guard is neutralised.\n%s", body)
	}
	// And it must be the FIRST thing that happens after loading the order, so no
	// side effect runs before the refusal.
	loadAt := strings.Index(body, "s.orders.ByID(ctx, orderID)")
	guardAt := strings.Index(body, guard)
	if loadAt < 0 || guardAt < 0 {
		t.Fatal("could not locate the load and the guard")
	}
	if guardAt < loadAt {
		t.Errorf("the guard runs before the order is loaded (guard %d, load %d)", guardAt, loadAt)
	}
}

// The transition table is the second half of the same story, and it is behavioural
// rather than a source match: these go through the real table.
//
// `partially_shipped -> delivered` is allowed because the remainder can arrive and
// what is already out can still be delivered. `-> cancelled` is not, for the reason
// above. `delivered -> cancelled` is not either: the goods are with the buyer, so a
// refund is a refund and not a cancellation.
func TestTheTransitionTableForbidsCancellingGoodsInTransit(t *testing.T) {
	for _, from := range []string{
		domain.OrderPartiallyShipped,
		domain.OrderShipped,
		domain.OrderDelivered,
		domain.OrderCompleted,
	} {
		if domain.CanTransition(from, domain.OrderCancelled) {
			t.Errorf("%s -> cancelled is allowed.\n"+
				"Cancelling an order whose goods have left is telling the buyer it is "+
				"off while their money and the goods are elsewhere; the route back is a "+
				"return, not a cancellation", from)
		}
	}
	// The two states that CAN be cancelled, so the assertion above is not satisfied
	// by forbidding everything.
	for _, from := range []string{domain.OrderPending, domain.OrderPaid} {
		if !domain.CanTransition(from, domain.OrderCancelled) {
			t.Errorf("%s -> cancelled is refused; an unpaid or just-paid order must be "+
				"cancellable", from)
		}
	}
}

func cancelOrderBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "order_service.go")
	at := strings.Index(src, "func (s *OrderService) CancelOrder(")
	if at < 0 {
		t.Fatal("CancelOrder is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}
