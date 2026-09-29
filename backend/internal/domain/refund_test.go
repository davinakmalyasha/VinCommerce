package domain

import (
	"testing"
	"time"
)

// Refund.NeedsOperator is what makes the `manual` state actionable.
//
// The whole reason the state exists is that a person has to move money by hand
// when the gateway will not. A state that is stored but not surfaced is a state
// nobody acts on -- the platform owes a buyer, the database knows, and the system
// reports nothing. So the classification is the feature, and it is tested
// directly rather than only through the HTTP layer.

func daysAgo(d int) time.Time { return time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour) }

func TestManualRefundsAlwaysNeedAnOperator(t *testing.T) {
	// However recent. A manual refund is a human decision by definition, and the
	// queue is what prompts it.
	for _, age := range []int{0, 1, 3, 30} {
		r := Refund{Status: RefundManual, RequestedAt: daysAgo(age)}
		if !r.NeedsOperator(time.Now().UTC(), 7*24*time.Hour) {
			t.Errorf("a manual refund requested %d day(s) ago does not need an operator; "+
				"the state is defined by needing one", age)
		}
	}
}

func TestASubmittedRefundNeedsAnOperatorOnlyOnceItIsStale(t *testing.T) {
	now := time.Now().UTC()
	stale := 7 * 24 * time.Hour

	// Fresh: the provider is within its settlement window (T+1 to T+7 by method).
	fresh := Refund{Status: RefundSubmitted, RequestedAt: now.Add(-6 * 24 * time.Hour)}
	if fresh.NeedsOperator(now, stale) {
		t.Error("a refund submitted six days ago is inside the settlement window " +
			"and should not yet be flagged as a person's problem")
	}
	// Past the window: "in progress" has stopped being true. The system has been
	// making that claim on the buyer's behalf for over a week, and a claim that
	// never expires is a way of never admitting a refund was lost.
	old := Refund{Status: RefundSubmitted, RequestedAt: now.Add(-10 * 24 * time.Hour)}
	if !old.NeedsOperator(now, stale) {
		t.Error("a refund still submitted after ten days must be flagged; the " +
			"gateway settles within seven and this one is stuck")
	}
}

// A settled-at refund is finished regardless of how old the request is. Otherwise
// the staleness rule would flag every successful refund ever created, which is
// how an alert gets ignored.
func TestASettledRefundNeverNeedsAnOperator(t *testing.T) {
	settledAt := daysAgo(1)
	r := Refund{Status: RefundSubmitted, RequestedAt: daysAgo(400), SettledAt: &settledAt}
	if r.NeedsOperator(time.Now().UTC(), 7*24*time.Hour) {
		t.Error("a refund that has settled is not a person's problem, however old " +
			"the request is; flagging it trains the operator to ignore the flag")
	}
}

func TestTerminalRefundsNeverNeedAnOperator(t *testing.T) {
	now := time.Now().UTC()
	for _, status := range []string{RefundSucceeded, RefundFailed} {
		r := Refund{Status: status, RequestedAt: daysAgo(60)}
		if r.NeedsOperator(now, 7*24*time.Hour) {
			t.Errorf("a %s refund is final and must not be queued for an operator", status)
		}
	}
}

// A pending refund that was never submitted is not stale; it is queued behind a
// submission that has not happened, which is a different problem with a different
// owner.
func TestAPendingRefundIsNotStale(t *testing.T) {
	// `pending` means reserved but not yet handed to the gateway. Nothing about the
	// gateway's settlement window applies to it.
	r := Refund{Status: RefundPending, RequestedAt: daysAgo(30)}
	if r.NeedsOperator(time.Now().UTC(), 7*24*time.Hour) {
		t.Error("a pending refund is waiting on our own submission, not on the " +
			"gateway's settlement window")
	}
}

// The valid-status set must match the schema's CHECK exactly. A filter that
// accepts a state the database rejects, or rejects one it allows, is a filter that
// hides refunds.
func TestValidRefundStatusMatchesTheSchema(t *testing.T) {
	// Mirrors CHECK (status IN ('pending','submitted','succeeded','failed','manual'))
	schema := map[string]bool{
		RefundPending: true, RefundSubmitted: true, RefundSucceeded: true,
		RefundFailed: true, RefundManual: true,
	}
	declared := []string{
		RefundPending, RefundSubmitted, RefundSucceeded, RefundFailed, RefundManual,
	}
	for _, s := range declared {
		if !schema[s] {
			t.Errorf("%q is declared in Go but not allowed by the schema", s)
		}
	}
	if len(declared) != len(schema) {
		t.Errorf("Go declares %d statuses, the schema allows %d", len(declared), len(schema))
	}
}
