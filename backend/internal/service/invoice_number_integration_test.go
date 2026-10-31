package service

import (
	"context"
	"strings"
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
	if !strings.HasPrefix(first, "INV-") || len(first) < 16 {
		t.Fatalf("invoice number %q does not look like INV-YYYYMM-NNNNNN", first)
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
}
