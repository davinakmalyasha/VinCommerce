package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

// The parcel sequence is a read-then-insert:
//
//	SELECT MAX(sequence) + 1     -- both transactions read 0
//	INSERT ... VALUES (1)         -- both write 1
//
// Under READ COMMITTED neither sees the other's uncommitted row, so both compute 1 and
// the second INSERT blocks on the UNIQUE index and then fails with a raw database error
// naming an index. Nothing corrupts -- but the seller who double-tapped gets an error he
// cannot act on, after a wait proportional to the first transaction.
//
// The lock must therefore be taken BEFORE the MAX is read. A lock after it would be
// worse than none: it would look like protection and provide none.
func TestSequenceAllocationIsLockedBeforeTheMaxIsRead(t *testing.T) {
	src := repoSource(t, "shipment_repo.go")
	body := shipmentFuncBody(t, src, "func (r *ShipmentRepository) CreateShipment(")

	lockAt := strings.Index(body, "pg_advisory_xact_lock")
	maxAt := strings.Index(body, "MAX(sequence)")
	insertAt := strings.Index(body, "INSERT INTO shipments")

	if lockAt < 0 {
		t.Fatal("CreateShipment takes no advisory lock.\n" +
			"The sequence is derived by reading MAX(sequence) and then inserting, so two " +
			"concurrent parcel creations for one order both compute the same number and " +
			"the second fails on the unique index")
	}
	if maxAt < 0 || insertAt < 0 {
		t.Fatal("CreateShipment no longer reads MAX(sequence) or inserts; update this test")
	}
	if lockAt > maxAt {
		t.Errorf("the lock is at offset %d but MAX(sequence) is read at %d: the lock is "+
			"taken AFTER the value it protects.\n"+
			"Serialising only the INSERT still leaves two transactions reading the same "+
			"MAX, so the lock has to cover the read", lockAt, maxAt)
	}
	if lockAt > insertAt {
		t.Error("the lock is taken after the insert")
	}

	// Two keys, not one. A single hash of order_id would serialise the outbound and
	// return sequence spaces against each other for no benefit; hashing the pair into
	// one key would too. Two arguments is what states the intent: per (order, kind).
	if !strings.Contains(body, "hashtext($1), hashtext($2)") {
		t.Errorf("the lock is not keyed on (order_id, kind):\n%s\n"+
			"Expected pg_advisory_xact_lock(hashtext($1), hashtext($2))", firstLines(body, 12))
	}
}

// The read-then-insert also produces a raw DB error rather than something a seller can
// act on. With the lock held that can only mean another writer, which is a retryable
// conflict and should be a domain error.
func TestSequenceConflictBecomesARetryableDomainError(t *testing.T) {
	body := shipmentFuncBody(t, repoSource(t, "shipment_repo.go"),
		"func (r *ShipmentRepository) CreateShipment(")

	if !strings.Contains(body, "isUniqueViolation(err)") {
		t.Error("a unique violation on the parcel insert is not translated.\n" +
			"Reaching it means something outside this function wrote a parcel while we " +
			"waited on the lock, and the seller should be told to reload and retry rather " +
			"than be handed a raw constraint name")
	}
	if !strings.Contains(body, "PARCEL_SEQUENCE_TAKEN") {
		t.Error("the conflict is not reported as a domain error with a stable code")
	}
	if !strings.Contains(body, "domain.KindConflict") {
		t.Error("the conflict should be a KindConflict so the caller maps it to 409")
	}
}

// THE WORSE OF THE TWO RACES.
//
// CreateReturnParcel refused a second parcel by reading
// `return_requests.return_shipment_id IS NOT NULL` -- but that read happened BEFORE
// CreateShipment took its lock, so it was a check performed outside the critical
// section. Two concurrent requests both read "no parcel yet", serialised on the lock
// further in, and the second never re-read.
//
// The write is an UPDATE of return_shipment_id, so the second OVERWROTE the first rather
// than conflicting: two return parcel rows existed and the return pointed at only one.
// A parcel of goods in motion that nothing tracked.
//
// The lock has to precede the check.
func TestReturnParcelIsCheckedUnderItsOwnLock(t *testing.T) {
	body := shipmentFuncBody(t, repoSource(t, "return_parcel_repo.go"),
		"func (r *ShipmentRepository) CreateReturnParcel(")

	lockAt := strings.Index(body, "pg_advisory_xact_lock")
	checkAt := strings.Index(body, "return_shipment_id IS NOT NULL")

	if lockAt < 0 {
		t.Fatal("CreateReturnParcel takes no advisory lock.\n" +
			"Two concurrent requests can both observe 'no parcel yet' and both create one, " +
			"because the write is an UPDATE and the second silently overwrites the first")
	}
	if checkAt < 0 {
		t.Fatal("CreateReturnParcel no longer refuses an existing parcel; update this test")
	}
	if lockAt > checkAt {
		t.Errorf("the lock is at offset %d but the existing-parcel check is at %d.\n"+
			"The check must run UNDER the lock. Before it, the check is not a guard at all: "+
			"both callers read 'none', then serialise, and the second proceeds without "+
			"re-reading -- orphaning a parcel", lockAt, checkAt)
	}

	// The lock must be keyed on the return id, not the order: this invariant is
	// per-return, whereas CreateShipment's is per-(order, kind).
	lockLine := body[lockAt:]
	if end := strings.Index(lockLine, "\n"); end > 0 {
		lockLine = lockLine[:end]
	}
	if !strings.Contains(lockLine, "returnID") {
		t.Errorf("the return-parcel lock is not keyed on the return id: %q\n"+
			"Reusing the (order, kind) key would serialise unrelated returns on the same "+
			"order for no benefit", strings.TrimSpace(lockLine))
	}
}

// The advisory locks are released at the end of the transaction, so they only mean
// anything if every caller actually opens one. Passing a pool silently restores the
// races above, because an xact lock outside a transaction block is a no-op.
func TestEveryParcelCreationCallerRunsInATransaction(t *testing.T) {
	cases := []struct{ file, fn string }{
		{"shipment_service.go", "func (s *ShipmentService) CreateParcel("},
		{"shipment_service.go", "func (s *ShipmentService) CreateReturnParcel("},
	}
	svc := repoSource(t, filepath.Join("..", "service", "shipment_service.go"))

	for _, c := range cases {
		body := shipmentFuncBody(t, svc, c.fn)
		beginAt := strings.Index(body, ".Begin(ctx)")
		commitAt := strings.Index(body, ".Commit(ctx)")
		if beginAt < 0 || commitAt < 0 {
			t.Errorf("%s does not open and commit a transaction.\n%s\n"+
				"pg_advisory_xact_lock is released at the end of the transaction, so with "+
				"the pool instead the sequence lock and the one-parcel lock are both no-ops",
				c.fn, firstLines(body, 10))
			continue
		}
		if beginAt > commitAt {
			t.Errorf("%s commits before it begins", c.fn)
		}
		if !strings.Contains(body, "tx.Querier()") {
			t.Errorf("%s does not pass tx.Querier() to the repository.\n%s\n"+
				"Passing the pool would silently disable both advisory locks",
				c.fn, firstLines(body, 14))
		}
	}
}

func shipmentFuncBody(t *testing.T, src, sig string) string {
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

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
