package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The first test in this package, and it exists because of a defect no test could
// reach.
//
// `CreateRefund` bound a LITERAL empty string to the `gateway_ref` column, ignoring
// the field on the struct it was given. `RecordProviderRefund` sets that field from
// the provider's own refund id, so the reference was silently dropped:
//
//	RecordProviderRefund -> refunds row with gateway_ref NULL
//	replayed webhook     -> RefundByProviderKey filters `gateway_ref IS NOT NULL`
//	                     -> no row can ever match
//	                     -> refundKeyFor(row) == ""  !=  the incoming key
//
// The replay guard was therefore inert for two independent reasons at once: it was
// asked about no payment (fixed separately), and the field it matches on was never
// stored. Fixing only the first would have left a guard that still never fires,
// while every test stayed green.
//
// These drive the real function and assert on the arguments it binds, which is the
// only place the loss was observable.

// recordingQuerier captures the statement and arguments a repository method runs.
type recordingQuerier struct {
	sql  string
	args []any
}

func (q *recordingQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.sql, q.args = sql, args
	return pgconn.CommandTag{}, nil
}

func (q *recordingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query is not used by CreateRefund")
}

func (q *recordingQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}

// gatewayRefArg is the bound value for the `gateway_ref` column, which is the
// fifth placeholder in the INSERT.
const gatewayRefArg = 4

// A refund the provider has already performed arrives with its own reference, and
// that reference is the entire basis of replay detection. Losing it makes every
// duplicate notification look new.
func TestCreateRefundPersistsTheProviderReference(t *testing.T) {
	q := &recordingQuerier{}
	repo := &PaymentRepository{}
	ref := "rf-77"

	if err := repo.CreateRefund(context.Background(), q, &Refund{
		ID:              "r1",
		PaymentIntentID: "intent-1",
		OrderID:         "o1",
		Gateway:         "midtrans",
		GatewayRef:      ref,
		Amount:          30000,
		Status:          "submitted",
	}); err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}

	if len(q.args) <= gatewayRefArg {
		t.Fatalf("INSERT bound %d arguments, want at least %d", len(q.args), gatewayRefArg+1)
	}
	got, ok := q.args[gatewayRefArg].(string)
	if !ok {
		t.Fatalf("gateway_ref bound as %T, want the reference string", q.args[gatewayRefArg])
	}
	if got != ref {
		t.Errorf("gateway_ref = %q, want %q\n"+
			"the replay lookup filters `gateway_ref IS NOT NULL` and rebuilds the key "+
			"from the stored row, so a dropped reference makes every replayed "+
			"notification look like a new refund and applies it again", got, ref)
	}
}

// The reserve path genuinely has no reference yet -- the provider assigns one when
// it accepts the refund -- and NULLIF turns the empty string into NULL. So
// honouring the field must not change the reserve path's behaviour.
func TestCreateRefundStoresNoReferenceWhenThereIsNoneYet(t *testing.T) {
	q := &recordingQuerier{}
	repo := &PaymentRepository{}

	if err := repo.CreateRefund(context.Background(), q, &Refund{
		ID:              "r1",
		PaymentIntentID: "intent-1",
		OrderID:         "o1",
		Gateway:         "midtrans",
		GatewayRef:      "",
		Amount:          30000,
		Status:          "pending",
	}); err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if got := q.args[gatewayRefArg]; got != "" {
		t.Errorf("gateway_ref = %#v, want an empty string so NULLIF writes NULL", got)
	}
	if !strings.Contains(q.sql, "NULLIF($5, '')") {
		t.Errorf("the INSERT no longer NULLIFs the reference:\n%s\n"+
			"an empty reference must be stored as NULL, or the replay lookup's "+
			"`gateway_ref IS NOT NULL` filter will match rows that identify nothing",
			q.sql)
	}
}

// End to end within the package: a row that kept its reference produces the key
// the service composes, which is the property the replay guard actually depends
// on. Without a stored reference the key is "" and can never match an incoming one.
func TestAStoredReferenceRebuildsTheReplayKey(t *testing.T) {
	const ref = "rf-77"

	if got := refundKeyFor(&Refund{Gateway: "midtrans", GatewayRef: ref, Amount: 30000}); got == "" {
		t.Error("a refund holding the provider's reference produced no key; the " +
			"replay lookup could never match it")
	} else if !strings.HasSuffix(got, ":"+ref+":30000") {
		t.Errorf("key = %q, want it to carry the reference and the amount", got)
	}

	if got := refundKeyFor(&Refund{Gateway: "midtrans", Amount: 30000}); got != "" {
		t.Errorf("a refund with no reference produced key %q; it identifies nothing "+
			"and must not be presented as a match", got)
	}
}
