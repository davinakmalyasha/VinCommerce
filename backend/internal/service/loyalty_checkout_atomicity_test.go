package service

import (
	"strings"
	"testing"
)

// funcBody returns the source of the function whose declaration contains `sig`,
// stopping at the next top-level `func `. It is deliberately textual: these tests
// assert that two calls are ordered relative to each other, which needs the source,
// not a running database.
func funcBody(t *testing.T, src, sig string) string {
	t.Helper()
	start := strings.Index(src, sig)
	if start < 0 {
		t.Fatalf("no declaration containing %q", sig)
	}
	rest := src[start:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		return rest[:end+1]
	}
	return rest
}

// The points debit must be part of the checkout's atomic unit.
//
// The defect this pins: `PlaceOrder` committed the order, then called
// `loyalty.Spend` -- a separate transaction on the pool -- and only logged a failure.
// Fifteen concurrent checkouts could each quote against the same 1,000-point balance,
// each apply the Rp 100,000 discount and each commit an order. The fifteen debits then
// serialised correctly and fourteen returned INSUFFICIENT_POINTS, against orders that
// were already durable. Fourteen free discounts.
//
// `Spend` itself was never wrong. It held the advisory lock and checked the balance
// correctly. The bug was that the protected thing lived in a different transaction
// from the protector, so no amount of correctness inside `Spend` could help.
//
// There is no database in this suite, so this asserts the property that can be
// asserted statically: the debit happens, it happens through the transaction, and it
// happens before the commit.
func TestLoyaltyDebitIsInsideTheCheckoutTransaction(t *testing.T) {
	body := funcBody(t, readSource(t, "order_service.go"), "func (s *OrderService) PlaceOrder(")

	debitAt := strings.Index(body, "SpendTx(")
	commitAt := strings.Index(body, "tx.Commit(ctx)")

	if debitAt < 0 {
		t.Fatal("PlaceOrder does not call SpendTx.\n" +
			"Redeemed points must be debited through the checkout transaction. If this is " +
			"now the pool-based Spend, the redemption is no longer atomic with the order " +
			"and concurrent checkouts can each redeem a balance they no longer hold")
	}
	if commitAt < 0 {
		t.Fatal("PlaceOrder no longer calls tx.Commit; this test needs updating")
	}
	if debitAt > commitAt {
		t.Errorf("SpendTx is at offset %d but tx.Commit is at %d: the debit runs AFTER the "+
			"order is durable, which is the double-spend this test exists to prevent.\n%s",
			debitAt, commitAt, body[max(0, debitAt-200):min(len(body), debitAt+300)])
	}

	// The debit must run in the CHECKOUT's transaction. Passing the repository would
	// silently reopen the original split, because the repository's methods acquire their
	// own pool connection.
	//
	// Note this accepts `tx.PgTx()` as well as `tx.Querier()`. Both are the same
	// transaction, so both are safe; the difference is only that PgTx hands the service
	// Commit and Rollback, which the codebase documents as an escape hatch it prefers to
	// keep closed. Asserting Querier specifically would report a style preference as a
	// safety failure, which is how these tests rot.
	if strings.Contains(body, "s.loyalty.Spend(") {
		t.Error("PlaceOrder calls the pool-based s.loyalty.Spend.\n" +
			"That method opens its own transaction on its own connection, so the debit is " +
			"outside the checkout's atomic unit -- the original double-spend")
	}
	debitLine := ""
	if ln := strings.Index(body, "s.loyalty.SpendTx("); ln >= 0 {
		end := strings.Index(body[ln:], "\n")
		if end < 0 {
			end = len(body) - ln
		}
		debitLine = body[ln : ln+end]
	}
	if debitLine == "" || (!strings.Contains(debitLine, "tx.Querier()") && !strings.Contains(debitLine, "tx.PgTx()")) {
		t.Errorf("SpendTx is not called with the checkout transaction:\n%s\n"+
			"It must receive tx.Querier() (preferred) or tx.PgTx(); anything else means the "+
			"debit is not part of the order's atomic unit", debitLine)
	}
}

// A failed debit must abort the checkout, not be logged and stepped over.
func TestLoyaltyDebitFailureAbortsCheckoutRatherThanLogging(t *testing.T) {
	body := funcBody(t, readSource(t, "order_service.go"), "func (s *OrderService) PlaceOrder(")

	guardAt := strings.Index(body, "if err := s.loyalty.SpendTx(")
	if guardAt < 0 {
		t.Fatal("PlaceOrder does not guard the SpendTx call")
	}
	end := strings.Index(body[guardAt:], "\n\t}")
	if end < 0 {
		t.Fatal("could not delimit the SpendTx error guard")
	}
	guard := body[guardAt : guardAt+end]

	if !strings.Contains(guard, "return nil, err") {
		t.Errorf("a failed points debit does not abort the checkout:\n%s\n"+
			"Returning is required. Logging and continuing leaves a committed order carrying "+
			"a discount nobody was charged for, which is the failure being fixed here", guard)
	}
	if strings.Contains(guard, "logger.Error") || strings.Contains(guard, "logger.Warn") {
		t.Errorf("the SpendTx error path still logs instead of failing:\n%s\n"+
			"Swallowing the error is what allowed fourteen free discounts through. There is "+
			"no order to reconcile any more -- the transaction rolls back", guard)
	}
}

// SpendTx must not open a transaction of its own or commit: that is the whole point of
// it, and it is easy to "fix" a nil-passing call by reaching for the pool.
func TestSpendTxJoinsTheCallersTransactionInsteadOfOpeningItsOwn(t *testing.T) {
	src := readRepositorySource(t, "loyalty_repo.go")

	start := strings.Index(src, "func (r *LoyaltyRepository) SpendTx(")
	if start < 0 {
		t.Fatal("loyalty_repo.go has no SpendTx method")
	}
	body := src[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	for _, forbidden := range []string{"Begin(", "Commit(", "r.pool.Acquire", "conn, err"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("SpendTx contains %q.\n%s\n"+
				"SpendTx must run inside the caller's transaction. Acquiring its own "+
				"connection, or beginning and committing, puts the debit back outside the "+
				"checkout's atomic unit -- which is the original defect", forbidden, body)
		}
	}

	// The advisory lock is load-bearing now: it is what stops two concurrent checkouts
	// for one user from both reading a pre-existing balance inside the transaction.
	if !strings.Contains(body, "pg_advisory_xact_lock") {
		t.Error("SpendTx lost the per-user advisory lock.\n" +
			"Without it, two concurrent checkouts can each read the same balance inside " +
			"their own transaction and both pass the check. The balance guard alone does " +
			"not help: READ COMMITTED evaluates it against a snapshot that the other " +
			"checkout's uncommitted debit does not appear in")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
