package domain

import "time"

// Refund is a durable record of one attempt to return money to a buyer, as the
// API exposes it.
//
// The persistence struct is `repository.Refund`; this is the wire shape. Two
// types for two audiences is deliberate: the repository type carries the
// identifiers the service needs to transact with, and this one carries only what
// an operator is allowed to see and is annotated for JSON. In particular
// `requested_by` is an internal user id and is exposed here only because an
// operator deciding whether to move money manually needs to know WHO asked.
//
// The states are the ones from migration 00043. `manual` is the one that earns
// this endpoint its existence: it means the platform owes the buyer, the gateway
// will not or cannot move it, and a human has to. A state nobody can see is a
// state nobody acts on.
type Refund struct {
	ID              string `json:"id"`
	PaymentIntentID string `json:"payment_intent_id"`
	OrderID         string `json:"order_id"`
	Gateway         string `json:"gateway"`
	// GatewayRef is the provider's id for THIS refund, which is distinct from the
	// charge it reverses. Exposed because it is the operator's reconciliation key
	// against the provider's own dashboard.
	GatewayRef string  `json:"gateway_ref,omitempty"`
	Amount     float64 `json:"amount"`
	Reason     string  `json:"reason,omitempty"`
	Status     string  `json:"status"`
	// FailureReason is populated for failed and manual refunds. It is the
	// difference between an operator guessing and an operator reading.
	FailureReason string     `json:"failure_reason,omitempty"`
	RequestedBy   string     `json:"requested_by,omitempty"`
	RequestedAt   time.Time  `json:"requested_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`

	// OrderNumber and BuyerEmail are denormalised for the queue view. An operator
	// triaging refunds needs to answer "who is owed money" without a second call
	// per row, and N+1 across a queue is what makes people export to a spreadsheet
	// and lose the audit trail.
	OrderNumber string `json:"order_number,omitempty"`
	BuyerEmail  string `json:"buyer_email,omitempty"`
}

// Refund states. These mirror the CHECK constraint on the refunds table; a state
// outside this set is rejected by the database, so the two cannot drift into
// disagreeing about what is legal.
const (
	RefundPending   = "pending"
	RefundSubmitted = "submitted"
	RefundSucceeded = "succeeded"
	RefundFailed    = "failed"
	// RefundManual means the platform owes the buyer and a human must transfer the
	// money. A real, expected state, not an error to hide: a refund that silently
	// failed is worse than one that is visibly stuck.
	RefundManual = "manual"
)

// RefundStates and the states that count toward a refund cap.
//
// The cumulative cap reads a literal list inside
// `PaymentRepository.SumRefundedByOrder`, and it used to be mirrored by a Go
// function in the service layer that NOTHING CALLED. Two definitions of the same
// rule, one of them dead, and a comment above the query claiming they "are read
// from the same Go constants the service writes, so the query and the writer
// cannot disagree". They could and did: the query is literals.
//
// The rule now lives here, where both packages can reach it, and the service-side
// mirror is gone. The query keeps its literals -- deriving an IN list at runtime
// would make the statement un-inspectable and the money-critical SQL less
// reviewable -- so `TestTheCapCountsExactlyTheStatesDomainSays` asserts the
// literals against this slice. Adding a state here therefore fails a test that
// names the query, rather than silently producing a cap that reads the wrong
// column of the refunds table.
//
// The rule, and why each state is in or out:
//
//	pending    NOT counted. Written before the provider is called; if the submit
//	           fails nothing moved, and counting it would refuse a legitimate
//	           retry.
//	submitted  counted. The provider accepted it and will pay. Not counting it
//	           would let a second refund be authorised while the first is in
//	           flight, which is precisely the double-refund the cap exists for.
//	succeeded  counted. The money is gone.
//	failed     NOT counted. The provider rejected it; no money moved.
//	manual     counted. We OWE the buyer this money and a human must send it.
//	           Not counting it would let the platform promise the same rupiah
//	           to two buyers, and it would do so while showing a clean cap.
var (
	// RefundStates is every legal refund state, in the order the CHECK constraint
	// lists them.
	RefundStates = []string{
		RefundPending, RefundSubmitted, RefundSucceeded, RefundFailed, RefundManual,
	}

	// RefundStatesCountingTowardCap is the subset that represents money that has
	// left, or is committed to leaving.
	RefundStatesCountingTowardCap = []string{
		RefundSubmitted, RefundSucceeded, RefundManual,
	}

	// RefundStatesNotCountingTowardCap is the complement, stated rather than
	// derived. Every legal state must appear in one slice or the other, and a test
	// says so -- a state in neither is one whose treatment was never decided, and
	// it would be decided by whichever branch the query happened to take.
	RefundStatesNotCountingTowardCap = []string{
		RefundPending, RefundFailed,
	}
)

// RefundStateCountsTowardCap reports whether a refund in this state represents
// money that has left, or is committed to leaving.
//
// Lives in domain rather than in the service because the rule is not the service's
// alone: the query that enforces it is in the repository, and a definition only the
// service can see is a definition the repository cannot be checked against.
func RefundStateCountsTowardCap(status string) bool {
	for _, s := range RefundStatesCountingTowardCap {
		if s == status {
			return true
		}
	}
	return false
}

// NeedsOperator reports whether this refund is waiting on a person rather than on
// a process.
//
// Deliberately not simply `status == manual`, and the two non-manual cases are
// different problems with different owners:
//
//   - `manual` is a human decision by definition, at any age.
//   - `submitted` has been handed to the gateway. The provider is expected to
//     settle within its window; past that, "in progress" has stopped being true.
//     The system has been making that claim on the buyer's behalf for over a
//     week, and a claim that never expires is a way of never admitting a refund
//     was lost.
//   - `pending` has NOT been handed to the gateway -- it is reserved but not yet
//     submitted, or is a platform-absorbed refund. Nothing about the gateway's
//     settlement window applies to it, so it is deliberately exempt. Applying the
//     staleness rule here would flag every pending row forever, and an alert that
//     always fires is an alert that is ignored.
func (r Refund) NeedsOperator(now time.Time, staleAfter time.Duration) bool {
	switch r.Status {
	case RefundManual:
		return true
	case RefundSubmitted:
		return r.SettledAt == nil && now.Sub(r.RequestedAt) > staleAfter
	}
	return false
}
