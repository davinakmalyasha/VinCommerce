package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// The cumulative cap counting the refund that is CURRENTLY BEING SETTLED.
//
// `SumRefundedByOrder` counts `submitted`, because a refund the provider has
// accepted will be paid. The reserve step therefore commits its row as
// `submitted` BEFORE the provider is called, and the settlement re-derives the cap
// afterwards -- so the row was in the table, counted, and charged to itself:
//
//	charge Rp100,000, full refund
//	reserve       -> refunds row, submitted, Rp100,000   (committed)
//	gateway       -> provider moves the money
//	completeRefund-> already = 100,000, remaining = 0, ALREADY_REFUNDED
//	                 tx rolls back; no legs reversed, no journal
//
// The buyer's money had already gone back. Every full refund failed, after the
// money left, and the row sat `submitted` for reconciliation to choke on.
//
// These tests drive the real `buildRefundPlan` against a fake that models the
// table, rather than asserting on the arithmetic. The cap's arithmetic was never
// the problem and already had tests; what was broken was the INPUT, and a test
// of the input is the only kind that can see it.

// fakeRefundRow is one row of the `refunds` table.
type fakeRefundRow struct {
	id     string
	amount float64
	status string
}

// capturedQuery is one statement the code under test actually executed.
type capturedQuery struct {
	sql  string
	args []any
}

// fakeRefundsDB answers the three queries buildRefundPlan makes, and records what
// it was asked.
//
// The sum honours the exclusion the query documents, implemented over the row set
// rather than by pattern-matching SQL. That is deliberately a SECOND
// implementation of the rule: if the SQL stops excluding, this fake keeps
// excluding, and `TestTheRefundBeingSettledIsExcludedByTheQuery` is what catches
// that. Between the two, neither half can regress alone.
type fakeRefundsDB struct {
	rows     []fakeRefundRow
	released bool
	queries  []capturedQuery
}

func (db *fakeRefundsDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("Exec is not used by the refund cap")
}

func (db *fakeRefundsDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query is not used by the refund cap")
}

func (db *fakeRefundsDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.queries = append(db.queries, capturedQuery{sql: sql, args: args})
	switch {
	case strings.Contains(sql, "FROM refunds"):
		return db.capRow(sql, args)
	case strings.Contains(sql, "FROM wallet_transactions"):
		// HasEscrowRelease
		return scanRow(func(dest ...any) error { return scanBool(dest, db.released) })
	case strings.Contains(sql, "FROM platform_fees"):
		return scanRow(func(dest ...any) error {
			return errors.New("the fee snapshot must be used; this intent already has one")
		})
	default:
		return scanRow(func(dest ...any) error {
			return fmt.Errorf("the refund cap issued an unexpected query: %s", sql)
		})
	}
}

// capRow models `SumRefundedByOrder`.
//
// It READS the exclusion clause out of the statement it was given rather than
// assuming one. That is the point: a mutation that flips the clause to
// `IS NOT NULL` still contains the text `id <> $2`, so a fake that merely looked
// for that substring would exclude the row anyway and the mutation would survive.
// Interpreting the clause means this fake answers the question the database would
// actually answer, so the test fails when the query stops excluding.
func (db *fakeRefundsDB) capRow(sql string, args []any) pgx.Row {
	// The clause is only an exclusion when $2 IS NULL. Inverted to `IS NOT NULL` it
	// is true for every settled row, so the row counts itself -- which is the
	// original defect, wearing a different hat.
	clause := ""
	switch {
	case strings.Contains(sql, "($2::uuid IS NOT NULL OR id <> $2::uuid)"):
		clause = "inverted"
	case strings.Contains(sql, "($2::uuid IS NULL OR id <> $2::uuid)"):
		clause = "excludes"
	default:
		clause = "absent"
	}

	bound := ""
	if len(args) > 1 && args[1] != nil {
		if s, ok := args[1].(string); ok {
			bound = s
		}
	}
	excluded := ""
	if clause == "excludes" && bound != "" {
		excluded = bound
	}

	total := 0.0
	for _, r := range db.rows {
		switch r.status {
		case RefundStateSubmitted, RefundStateSucceeded, RefundStateManual:
		default:
			continue
		}
		if excluded != "" && r.id == excluded {
			continue
		}
		total += r.amount
	}
	return scanRow(func(dest ...any) error { return scanFloat(dest, total) })
}

// lastCapQuery returns the cumulative-cap statement, or fails.
func (db *fakeRefundsDB) lastCapQuery(t *testing.T) capturedQuery {
	t.Helper()
	for i := len(db.queries) - 1; i >= 0; i-- {
		if strings.Contains(db.queries[i].sql, "FROM refunds") {
			return db.queries[i]
		}
	}
	t.Fatal("the cumulative cap was never read; the plan was built without consulting it")
	return capturedQuery{}
}

// scanRow adapts a function to pgx.Row.
type scanRow func(dest ...any) error

func (f scanRow) Scan(dest ...any) error { return f(dest...) }

func scanFloat(dest []any, v float64) error {
	if len(dest) != 1 {
		return fmt.Errorf("expected one destination for the cap, got %d", len(dest))
	}
	p, ok := dest[0].(*float64)
	if !ok {
		return fmt.Errorf("the cap scanned into %T, want *float64", dest[0])
	}
	*p = v
	return nil
}

func scanBool(dest []any, v bool) error {
	if len(dest) != 1 {
		return fmt.Errorf("expected one destination for the escrow check, got %d", len(dest))
	}
	p, ok := dest[0].(*bool)
	if !ok {
		return fmt.Errorf("the escrow check scanned into %T, want *bool", dest[0])
	}
	*p = v
	return nil
}

// chargedIntent is a captured Rp100,000 order whose commission snapshot exists,
// so the plan derives exact reversal legs without consulting the rate card.
func chargedIntent(orderID string) *domain.PaymentIntent {
	return &domain.PaymentIntent{
		ID:                 "intent-1",
		OrderID:            orderID,
		Amount:             100000,
		FeeAmount:          2000,
		SellerAmount:       98000,
		CommissionComputed: true,
		Currency:           "IDR",
	}
}

// The headline: a full refund must not be refused for including itself.
func TestAFullRefundIsNotChargedForItsOwnRow(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	// The reserve row, committed as `submitted` before the provider was called.
	db := &fakeRefundsDB{rows: []fakeRefundRow{
		{id: "r1", amount: 100000, status: RefundStateSubmitted},
	}}
	settling := "r1"

	plan, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 100000, &settling)
	if err != nil {
		t.Fatalf("a full refund was refused for counting its own row: %v\n"+
			"the provider had already moved the money, so this is a refund the buyer "+
			"never receives and the platform never books", err)
	}
	if plan.Amount != 100000 {
		t.Errorf("amount = Rp%.0f, want the full Rp100,000", plan.Amount)
	}
	// Remaining is what was available BEFORE this refund, so a full refund of an
	// untouched charge leaves it equal to the charge.
	if plan.Remaining != 100000 {
		t.Errorf("remaining = Rp%.0f, want the full Rp100,000 available before the refund",
			plan.Remaining)
	}
	if plan.Partial {
		t.Error("partial = true, but the whole charge was refunded")
	}
	if plan.NextStatus != domain.IntentRefunded {
		t.Errorf("next status = %q, want %q", plan.NextStatus, domain.IntentRefunded)
	}
}

// The query itself must carry the exclusion. The fake above stops excluding when
// this clause is gone, so without this test a revert of the SQL would be invisible
// to every other test in the file.
func TestTheRefundBeingSettledIsExcludedByTheQuery(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	db := &fakeRefundsDB{}
	settling := "refund-abc"

	if _, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 1000, &settling); err != nil {
		t.Fatalf("plan: %v", err)
	}
	q := db.lastCapQuery(t)

	if !strings.Contains(q.sql, "id <> $2") {
		t.Errorf("the cumulative query does not exclude the row being settled:\n%s\n"+
			"the reserve step committed it as `submitted`, which the cap counts, so "+
			"every full refund refuses itself after the provider has paid", q.sql)
	}
	// The clause must be the one that excludes when a row IS named. `IS NOT NULL`
	// reads as a near miss -- it still mentions the id, still compiles, and is
	// true for exactly the rows that must be counted.
	if !strings.Contains(q.sql, "($2::uuid IS NULL OR id <> $2::uuid)") {
		t.Errorf("the exclusion clause is not the one that excludes:\n%s\n"+
			"inverting the NULL test includes the row being settled and reinstates the "+
			"self-count, while looking like a correct filter to anything reading it "+
			"quickly", q.sql)
	}
	if len(q.args) < 2 {
		t.Fatalf("the exclusion is not bound to a parameter; args = %v", q.args)
	}
	bound, ok := q.args[1].(string)
	if !ok {
		t.Fatalf("exclusion argument is %T, want the refund's own id", q.args[1])
	}
	if bound != settling {
		t.Errorf("excluded %q, want the refund being settled (%q)", bound, settling)
	}
}

// The two SETTLEMENT functions are where the self-count actually happened, and
// they are the only two callers that re-derive the cap AFTER the row is in the
// table. Neither can be reached without a live database -- both open their own
// transaction -- so this asserts on their source.
//
// That is not a shortcut around the behavioural tests above; it covers the half
// those tests structurally cannot. `buildRefundPlan` can be perfect and
// `completeRefund` can still hand it nil, and the mutation M14 does exactly that.
// A guard whose input is never checked is the failure mode this whole workstream
// keeps rediscovering, so the wiring itself is pinned.
func TestEverySettlementExcludesItsOwnRowFromTheCap(t *testing.T) {
	// completeRefund re-derives after the provider confirmed; settleProviderRefund
	// re-derives after the provider told us it already paid. Both count `submitted`,
	// and both are settling a row they previously committed as `submitted`.
	for _, fn := range []string{"completeRefund", "settleProviderRefund"} {
		body := functionSource(t, "refund_service.go", fn)

		if !strings.Contains(body, "buildRefundPlan(ctx, q, locked, plan.Amount, &refund.ID)") &&
			!strings.Contains(body, "refundedTotal(ctx, q, order.ID, &refund.ID)") {
			t.Errorf("%s does not exclude its own refund row from the cap:\n%s\n"+
				"it re-derives the cumulative AFTER the row was committed as `submitted`, "+
				"so without the exclusion a full refund reads already=charge, finds "+
				"remaining=0 and refuses itself -- after the provider moved the money",
				fn, body)
		}
		if strings.Contains(body, "buildRefundPlan(ctx, q, locked, plan.Amount, nil)") {
			t.Errorf("%s passes nil for the exclusion; see above", fn)
		}
	}
}

// functionSource extracts one top-level function body from a file in this package.
// The lookup must be wired by the constructor, and there must be no second path.
//
// It used to be left nil, and the refund path quietly built the repository adapter
// itself on the way past. So every test drove a stub, production ran a different
// implementation, and the production path had no coverage at all -- which is how an
// empty intent id and a dropped reference both survived a green suite. One
// constructor, one implementation: whatever the tests drive is what ships.
func TestTheConstructorWiresTheRefundLookup(t *testing.T) {
	svc := NewPaymentService(&repository.PaymentRepository{}, nil, nil, "", "")

	if svc.refunds == nil {
		t.Fatal("NewPaymentService left the refund lookup unwired; the replay guard " +
			"would then run through a path no test exercises")
	}
	if _, ok := svc.refunds.(paymentRefundLookup); !ok {
		t.Errorf("lookup is %T, want paymentRefundLookup -- the same implementation "+
			"the repository-backed path uses, not a test-only substitute", svc.refunds)
	}
}

// A missing lookup must refuse, not fall back.
//
// The old fallback built the repository adapter from `s.payments`, which for an
// unwired service is nil: the guard would dereference nil and panic inside a
// webhook handler, or worse, be "fixed" later by a fallback that quietly diverges
// from what the tests cover. The honest response to not knowing whether a refund
// is a replay is to refuse -- proceeding applies the refund a second time.
func TestAMissingRefundLookupRefusesRatherThanFallingBack(t *testing.T) {
	// `payments` is nil, so any attempt to reach the database panics. The recover
	// turns that into a readable failure: a panic here is the old fallback
	// reaching for a repository that was never built, and saying so is more use
	// than a stack trace through three frames of plumbing.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the guard fell back to a repository that was never wired and "+
				"panicked (%v); a silent fallback is what made this path untestable in "+
				"the first place, and it must refuse instead", r)
		}
	}()

	svc := &PaymentService{}
	ev := providerRefundEvent{OrderID: "o1", Amount: 30000, Gateway: "midtrans", Reference: "rf-77"}

	_, err := svc.refundRecordedForProviderKey(t.Context(), "intent-1", providerRefundKey(ev))
	if err == nil {
		t.Fatal("an unwired refund lookup returned no error; the guard would treat " +
			"every notification as new and refund the buyer again")
	}
	if !strings.Contains(err.Error(), "REFUND_LOOKUP_UNWIRED") {
		t.Errorf("error = %v, want REFUND_LOOKUP_UNWIRED", err)
	}
}

// A racer that loses the claim must stop there.
//
// The claim (migration 00045) is what makes replay a database guarantee rather than
// a convention: the loser of a concurrent pair is TOLD it lost. Reporting the
// conflict as a claim instead means the loser goes on to settle the refund on top
// of the winner's -- the buyer is paid twice, and this time the database recorded
// only one row, so nothing downstream can detect it.
//
// `RecordProviderRefund` cannot be entered without a live database -- it reads the
// intent by order first -- so the ordering is pinned at the source: the losing
// branch must return BEFORE anything settles.
func TestALostClaimReturnsBeforeAnythingSettles(t *testing.T) {
	body := functionSource(t, "refund_service.go", "RecordProviderRefund")

	lostAt := strings.Index(body, "if !wasClaimed {")
	if lostAt < 0 {
		t.Fatalf("RecordProviderRefund does not check whether it won the claim:\n%s\n"+
			"without that check a racer that lost settles the refund again on top of "+
			"the winner's", body)
	}
	settleAt := strings.Index(body, "settleProviderRefund(")
	if settleAt < 0 {
		t.Fatalf("RecordProviderRefund never settles the refund it claimed:\n%s", body)
	}
	if lostAt > settleAt {
		t.Errorf("the claim is settled before the lost-claim check:\n%s\n"+
			"a racer that lost must return the winner's row, not settle a second one", body)
	}

	// And the branch must actually return rather than merely be present.
	branch := body[lostAt:]
	if end := strings.Index(branch, "\n\t}"); end > 0 {
		branch = branch[:end]
	}
	if !strings.Contains(branch, "return") {
		t.Errorf("the lost-claim branch does not return:\n%s\n"+
			"it must hand back the row that already holds the provider's reference, "+
			"which is also what stops the provider retrying", branch)
	}
}

// functionSource extracts one top-level function body from a file in this package.
func functionSource(t *testing.T, file, fn string) string {
	t.Helper()
	body := readSource(t, file)
	start := strings.Index(body, "func (s *PaymentService) "+fn+"(")
	if start < 0 {
		t.Fatalf("%s not found in %s", fn, file)
	}
	rest := body[start:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// An unscoped replay lookup must be refused before it reaches the database.
//
// `refunds.payment_intent_id` is `UUID NOT NULL`, so the lookup's
// `WHERE payment_intent_id = $1` given an empty string is `invalid input syntax for
// type uuid` -- a query ERROR, not an empty result. The caller aborts the whole
// refund, so every provider refund notification fails and is retried. Failing here
// with a named error says which wiring is wrong; failing in Postgres says only
// that something is.
func TestAReplayLookupWithoutAnIntentIsRefusedRatherThanQueried(t *testing.T) {
	lookup := &stubRefundLookup{found: &repository.Refund{ID: "should-not-match"}}
	svc := &PaymentService{refunds: lookup}
	ev := providerRefundEvent{OrderID: "o1", Amount: 30000, Gateway: "midtrans", Reference: "rf-77"}

	_, err := svc.refundRecordedForProviderKey(t.Context(), "  ", providerRefundKey(ev))
	if err == nil {
		t.Fatal("a replay lookup with a blank intent id was allowed to run; against " +
			"a UUID column that is a query error, and every provider refund " +
			"notification would fail")
	}
	if !strings.Contains(err.Error(), "REFUND_LOOKUP_WITHOUT_INTENT") {
		t.Errorf("error = %v, want REFUND_LOOKUP_WITHOUT_INTENT naming the missing scope", err)
	}
	if lookup.calls != 0 {
		t.Errorf("the database was consulted %d times despite the missing scope", lookup.calls)
	}
}

// An empty KEY is a different thing and stays a no-op: there is no provider
// reference, the notification is unidentifiable, and it is escalated to a human
// rather than deduplicated. Refusing it here would break the escalation path, so
// the two cases must not be collapsed.
func TestAnUnidentifiableNotificationStillSkipsTheLookup(t *testing.T) {
	lookup := &stubRefundLookup{found: &repository.Refund{ID: "should-not-match"}}
	svc := &PaymentService{refunds: lookup}

	got, err := svc.refundRecordedForProviderKey(t.Context(), "intent-1", "")
	if err != nil {
		t.Fatalf("an empty key was refused: %v", err)
	}
	if got != nil {
		t.Error("an empty key matched a refund; there is nothing to match on")
	}
	if lookup.calls != 0 {
		t.Errorf("an empty key hit the database %d times", lookup.calls)
	}
}

// The guard is only as good as the id its caller supplies, and
// `RecordProviderRefund` cannot be entered without a live database -- it reads the
// intent by order first. Pinned at the source for that reason, and because this is
// the seam that failed: the function knew the intent id and passed a literal "".
func TestTheNotificationPathScopesItsReplayLookupToThePaymentIntent(t *testing.T) {
	body := functionSource(t, "refund_service.go", "RecordProviderRefund")

	if !strings.Contains(body, "s.replayIsRecognised(ctx, intent.ID, ev)") {
		t.Errorf("RecordProviderRefund does not scope the replay lookup to the intent:\n%s\n"+
			"it has the intent in hand and must pass its id; an unscoped lookup is "+
			"either a uuid error on every notification or a search that matches "+
			"nothing, and both look like a working guard in a test with a stub", body)
	}
}

// Why exclude by ID rather than by state: an in-flight refund is money the
// provider accepted and will pay, and it must still count against a DIFFERENT
// refund. Dropping `submitted` from the cap instead would stop the self-count and
// reopen the race `03a100b` closed.
func TestAnInFlightRefundStillCountsAgainstEveryOtherRefund(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	db := &fakeRefundsDB{rows: []fakeRefundRow{
		{id: "r1", amount: 30000, status: RefundStateSubmitted},
		{id: "r2", amount: 30000, status: RefundStateSubmitted},
	}}
	settling := "r2"

	// Rp30,000 is already committed to the provider, so only Rp70,000 is left --
	// even though r2, the row being settled, is excluded from the total.
	if _, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 80000, &settling); err == nil {
		t.Fatal("a Rp80,000 refund was approved with Rp30,000 already in flight; " +
			"excluding the settling row must not un-count anyone else's")
	}
	plan, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 70000, &settling)
	if err != nil {
		t.Fatalf("the genuinely available Rp70,000 was refused: %v", err)
	}
	// Rp30,000 of r1 is still counted, which is the whole point of excluding by
	// id rather than by state. Were r1 dropped as well, the total would be zero.
	if plan.Remaining != 70000 {
		t.Errorf("remaining = Rp%.0f, want Rp70,000; r1's in-flight Rp30,000 must "+
			"still count against a different refund", plan.Remaining)
	}
	if plan.Partial {
		t.Error("partial = true, but the refund consumed everything that was left")
	}
}

// The reserve step and the return path have no row of their own in flight, so
// they exclude nothing -- and "nothing" must be SQL NULL, not an empty string.
// An empty string bound to a uuid parameter is a query ERROR, which would abort a
// legitimate refund rather than merely finding nothing.
func TestACapWithNoRowOfItsOwnExcludesNothing(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	db := &fakeRefundsDB{rows: []fakeRefundRow{
		{id: "r1", amount: 30000, status: RefundStateSucceeded},
	}}

	plan, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 0, nil)
	if err != nil {
		t.Fatalf("the reserve step's own cap read was refused: %v", err)
	}
	if plan.Amount != 70000 {
		t.Errorf("amount = Rp%.0f, want the whole remaining Rp70,000", plan.Amount)
	}
	arg := db.lastCapQuery(t).args[1]
	if arg != nil {
		t.Errorf("exclusion argument = %#v, want nil so the database reads SQL NULL; "+
			"an empty string here is a uuid cast error, not an absent filter", arg)
	}
}

// "No id" and "the empty id" are different claims. Only one of them is true, and
// guessing which is how a cap quietly stops counting.
func TestTheCapRefusesAnEmptyExclusionIdRatherThanGuessing(t *testing.T) {
	svc := &PaymentService{payments: &repository.PaymentRepository{}}
	db := &fakeRefundsDB{}
	empty := ""

	_, err := svc.buildRefundPlan(t.Context(), db, chargedIntent("o1"), 1000, &empty)
	if err == nil {
		t.Fatal("an empty exclusion id was accepted; a cap that cannot tell which " +
			"row to ignore is not enforcing anything")
	}
	for _, q := range db.queries {
		if strings.Contains(q.sql, "FROM refunds") {
			t.Error("the cap was queried despite the empty id; it must fail before " +
				"reaching the database rather than after")
		}
	}
}
