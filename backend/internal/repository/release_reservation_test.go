package repository

import (
	"os"
	"strings"
	"testing"
)

func orderRepoSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("order_repo.go")
	if err != nil {
		t.Fatalf("read order_repo.go: %v", err)
	}
	return string(raw)
}

func releaseReservationBody(t *testing.T) string {
	t.Helper()
	src := orderRepoSource(t)
	i := strings.Index(src, "func (t *OrderTx) ReleaseReservation(")
	if i < 0 {
		t.Fatal("no ReleaseReservation")
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		return body[:end]
	}
	return body
}

// THE DEFECT.
//
// The ledger insert was:
//
//	INSERT INTO stock_ledger (variant_id, order_id, change, reason)
//	SELECT variant_id, order_id, quantity, 'release'
//	  FROM inventory_reservations WHERE order_id = $1 AND status = 'released'
//
// `status = 'released'` selects EVERY release for the order, not the ones this call just
// transitioned. `tag.RowsAffected() > 0` only proves SOMETHING was released and then lets
// the INSERT reach back for rows an earlier release already wrote.
//
// `stock_ledger` is the audit trail for stock movements. An order with one reservation
// released earlier and one released now accounts for more stock returned than was ever
// held, and a reconciliation cannot distinguish that from a real second movement.
func TestTheLedgerInsertDoesNotSelectEveryReleasedReservation(t *testing.T) {
	body := releaseReservationBody(t)

	if strings.Contains(body, "WHERE order_id = $1 AND status = 'released'") {
		t.Errorf("ReleaseReservation still selects reservations by their resulting status.\n%s\n"+
			"That reads every release for the order, including any written by an earlier "+
			"path, and logs them again. The transitioned set has to come from the UPDATE's "+
			"RETURNING clause instead", body)
	}
	// The ledger rows must be driven by the in-memory set, not by re-querying.
	if !strings.Contains(body, "RETURNING variant_id::text, quantity") {
		t.Errorf("the status transition does not RETURN the rows it changed.\n%s\n"+
			"Without RETURNING there is no way to tell which reservations this call "+
			"released, which is the whole problem", body)
	}
}

// The restock and the ledger write must come from the SAME set. If the restock re-queried
// the table while the ledger used RETURNING, they could disagree -- and a disagreement
// between stock levels and the audit trail is worse than either being wrong alone.
func TestRestockAndLedgerAreDrivenByTheSameTransitions(t *testing.T) {
	body := releaseReservationBody(t)

	if !strings.Contains(body, "var got []released") {
		t.Error("the transitioned reservations are not collected.\n" +
			"There is nothing for the restock and the ledger write to share")
	}
	// No second broad SELECT/UPDATE keyed only on order_id + a status.
	if strings.Contains(body, "FROM inventory_reservations r") {
		t.Error("the restock still joins the reservations table by order.\n" +
			"Joining on status re-reads the same over-broad set the ledger did")
	}
	for _, field := range []string{"r.variantID, orderID, r.quantity"} {
		if !strings.Contains(body, field) {
			t.Errorf("the ledger insert does not use the collected set (%q).\n%s", field, body)
		}
	}
}

// A second call must be a no-op. Without the early return it would restock nothing but
// still reach the ledger insert.
func TestReleasingAnAlreadyReleasedOrderIsANoOp(t *testing.T) {
	body := releaseReservationBody(t)

	at := strings.Index(body, "if len(got) == 0 {")
	if at < 0 {
		t.Fatal("no early return for an empty transition set.\n" +
			"A second ReleaseReservation must not restock or log anything. Without the guard " +
			"it logs a duplicate for every prior release, which is the original defect")
	}
	restockAt := strings.Index(body, "UPDATE product_variants")
	ledgerAt := strings.Index(body, "INSERT INTO stock_ledger")
	if restockAt < 0 || ledgerAt < 0 {
		t.Fatal("restock or ledger write is missing; update this test")
	}
	if at > restockAt || at > ledgerAt {
		t.Error("the no-op guard runs after the writes it is meant to prevent")
	}
}

// Two releases touching overlapping variant sets lock rows in whatever order the query
// returned them. Without a sort, cart order can make that AB/BA and deadlock -- which is
// the checkout deadlocks the variant-lock ordering work also addresses.
func TestRestockLocksVariantsInASortedOrder(t *testing.T) {
	body := releaseReservationBody(t)

	if !strings.Contains(body, "sort.Strings(ids)") {
		t.Errorf("the restock does not sort variant ids.\n%s\n"+
			"Updating rows in query order means two concurrent releases over overlapping "+
			"sets can take the same two locks in opposite orders and deadlock", body)
	}
	sortAt := strings.Index(body, "sort.Strings(ids)")
	loopAt := strings.Index(body, "for _, id := range ids {")
	if sortAt > loopAt {
		t.Error("the ids are sorted after the loop that uses them")
	}
}

// The old shape tried to do this in one statement chain. Postgres executes sibling
// data-modifying CTEs concurrently, so the restock would not reliably see the rows the
// release produced.
func TestTheWriteIsNotExpressedAsChainedDataModifyingCtes(t *testing.T) {
	body := releaseReservationBody(t)
	if strings.Contains(body, "WITH ") && strings.Contains(body, "UPDATE inventory_reservations") {
		t.Errorf("ReleaseReservation chains data-modifying statements in a CTE.\n%s\n"+
			"PostgreSQL runs sibling data-modifying CTEs concurrently with unpredictable "+
			"order, so the restock may not observe the rows the release produced", body)
	}
}
