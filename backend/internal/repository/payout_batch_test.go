package repository

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A payout batch must never write `payouts.status`.
//
// `PayoutForUpdate`, `MarkPayoutSentTx` and `MarkPayoutFailedTx` all predicate on
// `WHERE ... AND status = 'pending'`. A batch that claimed its rows into `processing`
// would make them un-settleable AND un-failable at once, and their
// `seller_pending` balance -- credited when the withdrawal was requested, debited
// only by `payoutSettledEntries` or `payoutFailedEntries` -- would be stranded with
// no path back. The `payout` reservation would never be released either, so the
// seller's money would appear held forever.
//
// There is no repair for that anywhere in the codebase. So it is a stated invariant
// rather than a matter of care: `processing` and `cancelled` are legal in the CHECK
// and are deliberately never written.
//
// This is asserted over the WHOLE FILE rather than one method, because the failure
// mode is a plausible-looking line added to some future batch method. A per-method
// assertion would have to be extended every time a batch method is added, which is
// the same "five call sites each had to remember" shape that produced 0cb43d1.

// theOnlyPayoutStatusWrites are the two legitimate transitions: settling a
// withdrawal, and failing one. Both are one-payout operations with a real transfer
// reference, and both are guarded on `status = 'pending'` so they are idempotent
// against each other.
var theOnlyPayoutStatusWrites = []string{"status = 'sent'", "status = 'failed'"}

func TestNoBatchPathWritesThePayoutStatus(t *testing.T) {
	raw, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	body := string(raw)

	// Every `UPDATE payouts` statement, with enough context to be sure which one it
	// is. Splitting on the statement keyword rather than grepping a line, because a
	// statement wrapped over several lines would otherwise be missed entirely.
	var offending []string
	rest := body
	for {
		i := strings.Index(rest, "UPDATE payouts")
		if i < 0 {
			break
		}
		stmt := rest[i:]
		if end := strings.Index(stmt, "`"); end > 0 {
			stmt = stmt[:end]
		}
		legitimate := false
		for _, allowed := range theOnlyPayoutStatusWrites {
			if strings.Contains(stmt, allowed) {
				legitimate = true
				break
			}
		}
		if !legitimate {
			offending = append(offending, stmt)
		}
		rest = rest[i+len("UPDATE payouts"):]
	}

	for _, stmt := range offending {
		t.Errorf("a statement writes `payouts.status` outside the two settlement "+
			"transitions:\n%s\n"+
			"`PayoutForUpdate`, `MarkPayoutSentTx` and `MarkPayoutFailedTx` all "+
			"require status = 'pending'. Anything that moves a payout out of pending "+
			"makes it unsettleable and unfailable, and strands its seller_pending "+
			"balance with no repair", stmt)
	}

	// And the two that must exist, so the check cannot be satisfied by deleting them.
	for _, required := range theOnlyPayoutStatusWrites {
		if !strings.Contains(body, required) {
			t.Errorf("the legitimate settlement transition %q is gone; this test "+
				"exists to police what may write the column, not to delete the "+
				"transitions that are supposed to", required)
		}
	}
}

// The claim must exclude payouts already grouped into a live batch, and cancelling a
// batch must release them -- otherwise a cancelled batch's withdrawals are stranded
// and a payout could appear in two remittance files.
func TestAGroupedPayoutIsClaimedByExactlyOneLiveBatch(t *testing.T) {
	raw, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	body := string(raw)

	i := strings.Index(body, "func (r *PaymentRepository) UngroupedPayouts(")
	if i < 0 {
		t.Fatal("UngroupedPayouts not found")
	}
	fn := body[i:]
	if end := strings.Index(fn[1:], "\nfunc "); end > 0 {
		fn = fn[:end]
	}

	// The NOT EXISTS that is the claim, checked INSIDE this function and required to
	// be a CONJUNCT of the WHERE clause.
	//
	// Two earlier versions of this assertion were wrong in instructive ways.
	//
	// The first was `strings.Contains(body, "FROM payout_batch_items i")` against the
	// WHOLE FILE, which deleting the NOT EXISTS did not break, because the same
	// string appears in AddPayoutBatchItems. M35 survived it.
	//
	// The second required merely that "NOT EXISTS" appear in the function. M35
	// survived that too, because M35 rewrites the conjunct as
	//
	//     AND true OR NOT EXISTS (...)
	//
	// which still contains the phrase while defeating the claim: SQL binds AND
	// tighter than OR, so the exclusion stops being a condition on the row and the
	// grouped payouts come back.
	//
	// So the assertion is that the exclusion is ANDed into the WHERE -- not present,
	// but conjunctive. That is the property, and it is what a mutation has to break.
	if !strings.Contains(fn, "AND NOT EXISTS (") {
		t.Errorf("the exclusion is not a conjunct of the WHERE clause:\n%s\n"+
			"`AND true OR NOT EXISTS (...)` still reads like a claim but is not one: "+
			"AND binds tighter than OR, so a payout already grouped in a live batch "+
			"is selected again and paid twice from two remittance files", fn)
	}
	// A cancelled batch must NOT hold its claim: its withdrawals return to the
	// pool. Getting this wrong is safe-but-permanent -- every payout in a cancelled
	// batch could never be batched again.
	if !strings.Contains(fn, "b.status <> 'cancelled'") {
		t.Errorf("the claim does not exclude cancelled batches:\n%s\n"+
			"a cancelled batch releases its withdrawals, so treating it as live "+
			"strands every payout in it: it can never be batched again", fn)
	}
	// And the selection must be PENDING only.
	if !strings.Contains(fn, "p.status = 'pending'") {
		t.Errorf("the claim does not restrict to pending payouts:\n%s\n"+
			"a settled or failed payout in a remittance file is a second transfer "+
			"for money that already moved", fn)
	}
}

// The batch receipt is what makes a daily run re-runnable. `settlement_imports`
// learned this in ff15503 and says so at 00044:27-34; a payout batch has the
// identical problem.
func TestTheBatchReceiptMakesTheRunIdempotent(t *testing.T) {
	raw, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	body := string(raw)

	i := strings.Index(body, "func (r *PaymentRepository) CreatePayoutBatch(")
	if i < 0 {
		t.Fatal("CreatePayoutBatch not found")
	}
	fn := body[i:]
	if end := strings.Index(fn[1:], "\nfunc "); end > 0 {
		fn = fn[:end]
	}
	if !strings.Contains(fn, "ON CONFLICT (batch_ref)") {
		t.Errorf("the batch insert does not conflict on its ref:\n%s\n"+
			"a retried run would create a SECOND batch for the same cutoff and group "+
			"the same withdrawals again", fn)
	}
	if !strings.Contains(fn, "RETURNING id") {
		t.Errorf("the insert does not RETURN its id:\n%s\n"+
			"without it the caller cannot tell an insert from a conflict, and would "+
			"report a retried run as a fresh one", fn)
	}
	// An empty ref must be refused rather than creating an unnamed batch that the
	// scheduler's unique index cannot deduplicate.
	if !strings.Contains(fn, "PAYOUT_BATCH_REF_REQUIRED") {
		t.Errorf("an unnamed batch is allowed:\n%s\n"+
			"a NULL batch_ref is excluded from the unique index, so an unnamed batch "+
			"can be created any number of times with nothing to deduplicate them", fn)
	}
}

// The cached total must be recomputed from the items, not incremented. An
// incremental counter and the rows disagree after a retried run, and a `total` that
// disagrees with its own items is a number an operator trusts during approval.
func TestTheBatchTotalIsRecomputedRatherThanIncremented(t *testing.T) {
	raw, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	body := string(raw)
	i := strings.Index(body, "func (r *PaymentRepository) RecountPayoutBatch(")
	if i < 0 {
		t.Fatal("RecountPayoutBatch not found")
	}
	fn := body[i:]
	if end := strings.Index(fn[1:], "\nfunc "); end > 0 {
		fn = fn[:end]
	}
	if !strings.Contains(fn, "SUM(amount)") || !strings.Contains(fn, "COUNT(*)") {
		t.Errorf("the recount does not derive total and item_count from the rows:\n%s", fn)
	}

	// The right-hand side of the `total` assignment, matched EXACTLY.
	//
	// The first version of this test looked for the strings "total + " and
	// "item_count + ". That passes on `SET total = COALESCE(agg.sum, 0) + 1`,
	// which is the mutation -- the `+ 1` hangs off the closing paren, so neither
	// pattern ever appears. A negative assertion written to match a mutation one
	// guesses at is a false pass waiting to happen; matching the whole right-hand
	// side catches `+ 1`, `* 2`, a literal, or a different aggregate equally, and
	// cannot be satisfied by moving the arithmetic somewhere else in the line.
	// The right-hand side is taken as the WHOLE REST OF THE LINE with the trailing
	// comma stripped. The first attempt used `([^,\n]+)`, which stops at the comma
	// INSIDE `COALESCE(agg.sum, 0)` and compared `COALESCE(agg.sum` -- so the test
	// failed on the correct code. A capture class that cannot see a comma cannot
	// describe an expression that contains one.
	totalRHS := assignRHS(t, fn, "total")
	if totalRHS != "COALESCE(agg.sum, 0)" {
		t.Errorf("total is assigned %q, not the rows' own sum:\n%s\n"+
			"an incremental counter and the rows disagree after a retried run, and "+
			"the operator approves the number", totalRHS, fn)
	}

	// Same for the count.
	if got := assignRHS(t, fn, "item_count"); got != "COALESCE(agg.n, 0)" {
		t.Errorf("item_count is assigned %q, not the rows' own count:\n%s", got, fn)
	}
}

// assignRHS returns the expression assigned to `col` in a SET clause, trimmed and
// with its trailing comma removed.
//
// The assignment may follow `SET`, a comma, or the start of a line: gofmt wraps a
// SET clause that is too long onto one line per column, and the LAST column carries
// no trailing comma to anchor on.
func assignRHS(t *testing.T, fn, col string) string {
	t.Helper()
	re := regexp.MustCompile(`(?:^|SET|,)\s*` + col + `\s*=\s*(.+)$`)
	for _, line := range strings.Split(fn, "\n") {
		if m := re.FindStringSubmatch(strings.TrimRight(line, " \t\r")); m != nil {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[1]), ","))
		}
	}
	t.Fatalf("no `SET %s = ...` assignment found", col)
	return ""
}
