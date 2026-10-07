package service

import (
	"strings"
	"testing"
)

// THE DEFECT.
//
// PlaceOrder reserved stock by walking the cart line by line, so the row-lock order
// was whatever order the buyer happened to have the lines in. Two concurrent
// checkouts containing the same two variants -- the flash-sale case, two people
// buying the last units of two popular items at once -- then take those locks in
// opposite orders and deadlock:
//
//	TX A locks variant 1, waits for variant 2
//	TX B locks variant 2, waits for variant 1
//
// Each transaction is individually correct, which is precisely why it does not show
// up in testing. Both connections sit until statement_timeout and the buyer gets an
// error on a checkout that should have been trivial.
func TestCheckoutLocksEveryVariantBeforeReservingAny(t *testing.T) {
	src := readSource(t, "order_service.go")

	lockAt := strings.Index(src, "tx.LockVariants(")
	reserveAt := strings.Index(src, "tx.ReserveStock(")
	if lockAt < 0 {
		t.Fatal("PlaceOrder no longer locks variants up front.\n" +
			"ReserveStock locks one row at a time, so without a pre-lock the lock order " +
			"is the buyer's cart order and two concurrent checkouts deadlock")
	}
	if reserveAt < 0 {
		t.Fatal("no ReserveStock call found; update this test")
	}
	if lockAt > reserveAt {
		t.Errorf("LockVariants is called AFTER the first ReserveStock (lock at byte %d, reserve at %d).\n"+
			"Locking after some locks are already held does not fix the ordering: "+
			"the first rows are already taken in cart order", lockAt, reserveAt)
	}
}

// The ids passed to LockVariants must be the ones the reserve loop uses. Locking a
// different set -- a subset, or a list built before the bundle is final -- leaves the
// offending rows unguarded while looking like the fix is in place.
func TestLockVariantsIsFedTheSameBundleTheReserveLoopWalks(t *testing.T) {
	src := readSource(t, "order_service.go")

	lockCall := strings.Index(src, "tx.LockVariants(")
	if lockCall < 0 {
		t.Fatal("no LockVariants call")
	}
	// The window between building the id list and calling LockVariants.
	start := lockCall - 400
	if start < 0 {
		start = 0
	}
	window := src[start:lockCall]
	if !strings.Contains(window, "range bundle") {
		t.Errorf("the lock set is not built by walking `bundle`.\n%s\n"+
			"Whatever list it walks, the reserve loop walks `bundle`; they have to be "+
			"the same set or the lock guards rows that are not reserved", window)
	}
	if !strings.Contains(window, "l.VariantID") {
		t.Errorf("the lock set does not take VariantID off the bundle line.\n%s", window)
	}
}

// A lock taken in arbitrary order is the original bug with an extra step. The sort is
// the whole point: it makes lock order a function of the variant ids alone, so two
// transactions cannot disagree about who goes first.
func TestLockVariantsSortsBeforeLocking(t *testing.T) {
	repo := readRepositorySource(t, "order_repo.go")

	at := strings.Index(repo, "func (t *OrderTx) LockVariants(")
	if at < 0 {
		t.Fatal("LockVariants is missing from order_repo.go")
	}
	end := strings.Index(repo[at+1:], "\nfunc ")
	if end < 0 {
		end = len(repo) - at
	}
	body := repo[at : at+end]

	if !strings.Contains(body, "sort.Strings(ids)") {
		t.Errorf("LockVariants does not sort the ids.\n%s\n"+
			"Locking in argument order leaves the lock order up to the caller, which is "+
			"cart order, which is the deadlock", body)
	}
	sortAt := strings.Index(body, "sort.Strings(ids)")
	lockAt := strings.Index(body, "FOR UPDATE")
	if lockAt < 0 {
		t.Fatalf("LockVariants contains no FOR UPDATE at all:\n%s", body)
	}
	if sortAt > lockAt {
		t.Errorf("the ids are sorted AFTER the lock is taken (sort at %d, lock at %d)", sortAt, lockAt)
	}
}

// Sorted but still one statement that could let the plan decide the order.
// `WHERE id = ANY($1) ORDER BY id FOR UPDATE` may be served by a Sort node over
// whatever order the index produced, and then the lock order is the plan's business.
// One statement per id in a Go loop is unambiguous.
func TestLockVariantsDoesNotDelegateOrderingToThePlan(t *testing.T) {
	repo := readRepositorySource(t, "order_repo.go")

	at := strings.Index(repo, "func (t *OrderTx) LockVariants(")
	if at < 0 {
		t.Fatal("LockVariants is missing")
	}
	end := strings.Index(repo[at+1:], "\nfunc ")
	if end < 0 {
		end = len(repo) - at
	}
	body := repo[at : at+end]

	if strings.Contains(body, "ANY($1)") || strings.Contains(body, "ANY($") {
		t.Errorf("LockVariants locks with a set predicate.\n%s\n"+
			"With ANY() Postgres may satisfy ORDER BY using a Sort node over index order, "+
			"so the lock order is the plan's, not ours. Lock one id per statement instead", body)
	}
	if !strings.Contains(body, "for _, id := range ids {") {
		t.Errorf("LockVariants does not lock ids one at a time in a Go loop.\n%s", body)
	}
}

// A variant id with no row is not this function's error to report. ReserveStock has
// always been the one that fails on a missing variant, and moving that failure here
// would change what the buyer sees.
func TestLockVariantsLeavesAMissingVariantToReserveStock(t *testing.T) {
	repo := readRepositorySource(t, "order_repo.go")

	at := strings.Index(repo, "func (t *OrderTx) LockVariants(")
	if at < 0 {
		t.Fatal("LockVariants is missing")
	}
	end := strings.Index(repo[at+1:], "\nfunc ")
	if end < 0 {
		end = len(repo) - at
	}
	body := repo[at : at+end]

	// Match the CALL, not the word: the doc comment here says "not QueryRow" in
	// prose, and a bare Contains("QueryRow") failed on its own explanation -- a test
	// reading its own comment as code.
	if strings.Contains(body, ".QueryRow(") {
		t.Errorf("LockVariants calls QueryRow, which errors on a missing variant.\n%s\n"+
			"That moves ErrNoRows out of ReserveStock and changes the failure the buyer sees", body)
	}
	if !strings.Contains(body, ".Exec(") {
		t.Errorf("LockVariants does not use Exec, so a missing variant would be an error here.\n%s", body)
	}
}
