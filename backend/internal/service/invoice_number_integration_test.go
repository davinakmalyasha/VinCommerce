package service

import (
	"context"
	"regexp"
	"strconv"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// An invoice number is a fiscal reference, not a display string: two views of
// the same order must not consume two numbers, and two orders must not share
// one. The UPDATE ... WHERE invoice_number IS NULL is the mechanism; this test
// is the proof it is wired.
func TestInvoiceNumberIsAssignedOnce(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	orders := repository.NewOrderRepository(pool)

	rows, err := pool.Query(ctx, `
		SELECT id::text FROM orders
		 WHERE invoice_number IS NULL
		 ORDER BY placed_at DESC LIMIT 2`)
	if err != nil {
		t.Fatalf("find orders: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan order id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate orders: %v", err)
	}
	if len(ids) < 2 {
		t.Fatalf("need two orders without an invoice number, found %d", len(ids))
	}

	first, err := orders.AssignInvoiceNumber(ctx, ids[0])
	if err != nil {
		t.Fatalf("first assignment: %v", err)
	}
	format := regexp.MustCompile(`^INV-(\d{4})-(\d{6})$`)
	if !format.MatchString(first) {
		t.Fatalf("invoice number %q does not look like INV-YYYY-NNNNNN", first)
	}

	// The second view must return the SAME number, not consume another.
	again, err := orders.AssignInvoiceNumber(ctx, ids[0])
	if err != nil {
		t.Fatalf("second view: %v", err)
	}
	if again != first {
		t.Fatalf("a second view issued %q, first was %q", again, first)
	}

	second, err := orders.AssignInvoiceNumber(ctx, ids[1])
	if err != nil {
		t.Fatalf("assignment for the second order: %v", err)
	}
	if second == first {
		t.Fatalf("two orders received the same invoice number %q", first)
	}

	// Same fiscal year, issued consecutively: the counter must advance by
	// exactly one. A gap here is what an auditor asks about.
	firstParts := format.FindStringSubmatch(first)
	secondParts := format.FindStringSubmatch(second)
	if firstParts[1] == secondParts[1] {
		a, _ := strconv.Atoi(firstParts[2])
		b, _ := strconv.Atoi(secondParts[2])
		if b != a+1 {
			t.Fatalf("consecutive invoices in %s are %q then %q (counter jumped)",
				firstParts[1], first, second)
		}
	}
}
