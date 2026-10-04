package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// recordingQuerier captures every statement a repository method runs, and answers
// each QueryRow from a queue.
type recordingQuerier struct {
	sql   string
	args  []any
	row   pgx.Row
	stmts []string
}

func (q *recordingQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.sql, q.args = sql, args
	q.stmts = append(q.stmts, sql)
	return pgconn.CommandTag{}, nil
}

func (q *recordingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query is not used by these tests")
}

func (q *recordingQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql, q.args = sql, args
	q.stmts = append(q.stmts, sql)
	return q.row
}

// twoColumnRow answers `(balance, held_balance) RETURNING ...`, which is what the
// hold's wallet UPDATE returns.
type twoColumnRow struct{}

func (twoColumnRow) Scan(dest ...any) error {
	if len(dest) != 2 {
		return errors.New("expected two destinations")
	}
	p0, ok0 := dest[0].(*float64)
	p1, ok1 := dest[1].(*float64)
	if !ok0 || !ok1 {
		return errors.New("expected *float64 destinations")
	}
	*p0, *p1 = 70000, 30000
	return nil
}

// idRow answers `RETURNING id`.
type idRow struct{}

func (idRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return errors.New("expected one destination")
	}
	p, ok := dest[0].(*string)
	if !ok {
		return errors.New("expected a *string destination")
	}
	*p = "00000000-0000-0000-0000-0000000000ff"
	return nil
}

// readMigrationDir returns every migration's text, for tests that assert on the
// schema rather than on a single file.
func readMigrationDir(t *testing.T) ([]struct {
	name string
	text string
}, error) {
	dir := filepath.Join("..", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []struct {
		name string
		text string
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, struct {
			name string
			text string
		}{e.Name(), string(raw)})
	}
	return out, nil
}

// rowsQueueQuerier answers successive QueryRow calls, which is what the claim's
// "insert, and if it conflicted, read the winner back" sequence needs.
type rowsQueueQuerier struct {
	recordingQuerier
	queued []pgx.Row
}

func (q *rowsQueueQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql, q.args = sql, args
	q.stmts = append(q.stmts, sql)
	if len(q.queued) == 0 {
		return noRowsRow{}
	}
	row := q.queued[0]
	q.queued = q.queued[1:]
	return row
}

// noRowsRow is what `ON CONFLICT DO NOTHING RETURNING` yields on a conflict: a
// successful statement with no row. scanRefund turns it into domain.ErrNotFound.
type noRowsRow struct{}

func (noRowsRow) Scan(dest ...any) error { return pgx.ErrNoRows }

// refundRow builds a row in the shape scanRefund expects, so a claim can be
// exercised end to end without a database.
type refundRow struct {
	rf Refund
}

func (r refundRow) Scan(dest ...any) error {
	if len(dest) != 14 {
		return errors.New("unexpected column count")
	}
	*dest[0].(*string) = r.rf.ID
	*dest[1].(*string) = r.rf.PaymentIntentID
	*dest[2].(*string) = r.rf.OrderID
	*dest[3].(*string) = r.rf.JournalID
	*dest[4].(*string) = r.rf.Gateway
	*dest[5].(*string) = r.rf.GatewayRef
	*dest[6].(*float64) = r.rf.Amount
	*dest[7].(*string) = r.rf.Reason
	*dest[8].(*string) = r.rf.Status
	*dest[9].(*string) = r.rf.FailureReason
	*dest[10].(*string) = r.rf.RequestedBy
	*dest[11].(*time.Time) = r.rf.RequestedAt
	*dest[12].(**time.Time) = r.rf.SettledAt
	*dest[13].(*time.Time) = r.rf.CreatedAt
	return nil
}

func providerRefundFixture() *Refund {
	return &Refund{
		ID: "r-new", PaymentIntentID: "intent-1", OrderID: "o1",
		Gateway: "midtrans", GatewayRef: "rf-77", Amount: 30000, Status: "submitted",
		RequestedAt: time.Now().UTC(),
	}
}

// The claim must be ONE statement against the unique index, not a SELECT followed
// by an INSERT. Two identical webhooks arriving together both read "not recorded"
// and both inserted; Midtrans retries for up to 24 hours and does not serialise
// its retries, so that race is winnable and the loser refunded the buyer twice.
//
// The conflict target repeats the index's predicate because that is how Postgres
// infers a PARTIAL unique index. Omit it and the insert does not deduplicate at
// all -- which looks exactly like a working guard while being no guard at all.
func TestTheProviderRefundIsClaimedInOneStatement(t *testing.T) {
	q := &rowsQueueQuerier{queued: []pgx.Row{
		refundRow{rf: *providerRefundFixture()},
	}}
	repo := &PaymentRepository{}

	existing, claimed, err := repo.ClaimProviderRefund(context.Background(), q, providerRefundFixture())
	if err != nil {
		t.Fatalf("ClaimProviderRefund: %v", err)
	}
	if !claimed {
		t.Error("a fresh reference was not claimed")
	}
	if existing == nil || existing.ID != "r-new" {
		t.Errorf("claimed row = %+v, want the row just written", existing)
	}

	if !strings.Contains(q.sql, "ON CONFLICT (gateway, gateway_ref) WHERE gateway_ref IS NOT NULL") {
		t.Errorf("the claim does not target the partial unique index:\n%s\n"+
			"Postgres requires the index predicate in the conflict target to infer a "+
			"partial index; without it the insert does not deduplicate and every "+
			"replayed webhook writes another row", q.sql)
	}
	if !strings.Contains(q.sql, "DO NOTHING") {
		t.Errorf("the claim conflicts rather than deferring:\n%s\n"+
			"a losing racer must be told it lost, not fail the refund", q.sql)
	}
	if !strings.Contains(q.sql, "RETURNING") {
		t.Errorf("the claim returns nothing:\n%s\n"+
			"the caller needs to know whether it won, and reading back separately "+
			"reopens the window this closes", q.sql)
	}
}

// A losing racer must get the winner's row back, not an error and not a second
// write. Returning the winner is also what stops the provider retrying.
func TestALostClaimReturnsTheWinningRow(t *testing.T) {
	winner := providerRefundFixture()
	winner.ID = "r-first"
	q := &rowsQueueQuerier{queued: []pgx.Row{
		noRowsRow{},            // the insert conflicted
		refundRow{rf: *winner}, // read the winner back
	}}
	repo := &PaymentRepository{}

	existing, claimed, err := repo.ClaimProviderRefund(context.Background(), q, providerRefundFixture())
	if err != nil {
		t.Fatalf("a lost claim returned an error; the provider would retry forever: %v", err)
	}
	if claimed {
		t.Error("a conflicting claim reported itself as the winner")
	}
	if existing == nil || existing.ID != "r-first" {
		t.Errorf("returned %+v, want the row that already held the reference", existing)
	}
	if !strings.Contains(q.sql, "WHERE gateway = $1 AND gateway_ref = $2") {
		t.Errorf("the read-back does not identify the row by provider reference:\n%s", q.sql)
	}
}

// A claim with no reference cannot be made idempotent, and inserting it would
// create a row that no replay could ever match -- the shape of the defect this
// whole sequence was fixing. The unidentifiable case is escalated instead.
func TestAReferenceIsRequiredToClaimARefund(t *testing.T) {
	q := &rowsQueueQuerier{}
	repo := &PaymentRepository{}

	_, claimed, err := repo.ClaimProviderRefund(context.Background(), q,
		&Refund{ID: "r", PaymentIntentID: "i", OrderID: "o", Gateway: "midtrans", Amount: 1000})
	if err == nil {
		t.Fatal("a provider refund with no reference was claimable; nothing could " +
			"ever match it, so it is a row that makes the guard look busy")
	}
	if claimed {
		t.Error("a claim with no reference reported success")
	}
	if len(q.queued) != 0 {
		t.Error("the database was queried despite the missing reference")
	}
}

// The read-back failing is not recoverable by guessing. The conflicting row is
// committed, so it must be findable; if it is not, the caller cannot tell a replay
// from a new refund, and guessing is a duplicate refund.
func TestAnUnreadableConflictRefusesRatherThanGuessing(t *testing.T) {
	q := &rowsQueueQuerier{queued: []pgx.Row{
		noRowsRow{}, // conflicted
		noRowsRow{}, // and the winner cannot be read
	}}
	repo := &PaymentRepository{}

	_, claimed, err := repo.ClaimProviderRefund(context.Background(), q, providerRefundFixture())
	if err == nil {
		t.Fatal("an unreadable conflict returned no error; the caller would treat " +
			"this as a new refund and pay the buyer twice")
	}
	if claimed {
		t.Error("an unreadable conflict reported a successful claim")
	}
	if !strings.Contains(err.Error(), "REFUND_CLAIM_CONFLICT_UNREADABLE") {
		t.Errorf("error = %v, want REFUND_CLAIM_CONFLICT_UNREADABLE", err)
	}
}

// The refund row is read by two different statements -- RETURNING on the claim and
// SELECT everywhere else. They must select the same columns in the same order, or
// scanRefund assigns a value to the wrong field on whichever path is rarer. A
// hand-copied column list is a second place to forget a column, which is why both
// go through one constant.
func TestTheClaimAndTheReadsShareOneColumnList(t *testing.T) {
	src := readPaymentRepoSource(t)

	if !strings.Contains(src, "const refundSelect = `SELECT ` + refundSelectColumns") {
		t.Error("refundSelect no longer selects refundSelectColumns; the claim's " +
			"RETURNING and the reads can now disagree about shape or order, and " +
			"scanRefund scans by position")
	}
	if !strings.Contains(src, "RETURNING `+refundSelectColumns") {
		t.Error("the claim does not RETURN the shared column list; it has its own, " +
			"and the two lists will drift")
	}
}

// The Go conflict target and the SQL index predicate are the same rule written
// twice, in two languages, and Postgres will not tell you they disagree -- it will
// simply fail to infer the index and the INSERT stops deduplicating while still
// succeeding. This asserts they match.
//
// Migration 00045 has never run against a real database, so this is the only place
// the two are compared at all.
func TestTheConflictTargetMatchesTheIndexItInfers(t *testing.T) {
	mig, err := os.ReadFile(filepath.Join("..", "db", "migrations", "00045_refund_replay_index.sql"))
	if err != nil {
		t.Fatalf("read migration 00045: %v", err)
	}
	sql := string(mig)

	const wantPredicate = "WHERE gateway_ref IS NOT NULL"
	// Matched on the INDEX NAME, not on the whole CREATE prefix.
	//
	// `CREATE UNIQUE INDEX idx_...` stopped matching when 00045 gained
	// `IF NOT EXISTS` as part of making the chain re-runnable, and the failure mode is
	// the dangerous one: a test that pins an incidental prefix of a DDL statement
	// breaks on an improvement, and the obvious "fix" is to weaken the assertion until
	// it passes again. The property under test is that this index EXISTS and is the
	// one the ON CONFLICT target infers -- the name is what identifies it.
	if !strings.Contains(sql, "idx_refunds_gateway_ref_unique") {
		t.Fatalf("00045 does not create the unique index:\n%s", sql)
	}
	if !strings.Contains(sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_refunds_gateway_ref_unique") &&
		!strings.Contains(sql, "CREATE UNIQUE INDEX idx_refunds_gateway_ref_unique") {
		t.Errorf("idx_refunds_gateway_ref_unique exists but is not a UNIQUE INDEX:\n%s", sql)
	}
	if !strings.Contains(sql, wantPredicate) {
		t.Errorf("00045's unique index is not partial on a present reference:\n%s\n"+
			"a refund we initiate has no reference yet, so the index must exclude "+
			"those rows", sql)
	}
	if !strings.Contains(sql, "ON refunds (gateway, gateway_ref)") {
		t.Errorf("00045's unique index is not on (gateway, gateway_ref):\n%s\n"+
			"two gateways may legitimately issue the same refund id, so the gateway "+
			"is part of the identity", sql)
	}
	if !strings.Contains(sql, "HAVING count(*) > 1") {
		t.Errorf("00045 does not refuse to build over existing duplicates:\n%s\n"+
			"de-duplicating would delete the record of money that left, so the "+
			"migration must stop and name them instead", sql)
	}

	// The claim's conflict target, verbatim, from the INSERT (the first of the two
	// statements the claim issues).
	q := &rowsQueueQuerier{queued: []pgx.Row{noRowsRow{}}}
	if _, _, err := (&PaymentRepository{}).ClaimProviderRefund(
		context.Background(), q, providerRefundFixture()); err == nil {
		t.Fatal("expected the unreadable-conflict error")
	}
	if len(q.stmts) == 0 {
		t.Fatal("the claim issued no statement")
	}
	insert := q.stmts[0]
	if !strings.Contains(insert, "ON CONFLICT (gateway, gateway_ref) "+wantPredicate) {
		t.Errorf("the claim's conflict target does not match the index's predicate:\n%s\n"+
			"Postgres infers a PARTIAL unique index only when the predicate is repeated "+
			"here. Omit it and the insert stops deduplicating -- silently, and while "+
			"still returning a row", insert)
	}
}

func readPaymentRepoSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	return string(raw)
}

// repo00045 exists so the migration test reads as a statement about the claim
// rather than about constructing a repository.
func TestReadPaymentRepoSourceIsAvailable(t *testing.T) {
	if !strings.Contains(readPaymentRepoSource(t), "ClaimProviderRefund") {
		t.Error("payment_repo.go no longer contains ClaimProviderRefund")
	}
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
