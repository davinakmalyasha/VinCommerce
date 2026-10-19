package repository

import (
	"os"
	"strings"
	"testing"
)

func cartRepoSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("cart_repo.go")
	if err != nil {
		t.Fatalf("read cart_repo.go: %v", err)
	}
	return string(raw)
}

func updateItemBody(t *testing.T) string {
	t.Helper()
	src := cartRepoSource(t)
	i := strings.Index(src, "func (r *CartRepository) UpdateItem(")
	if i < 0 {
		t.Fatal("no UpdateItem")
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		return body[:end]
	}
	return body
}

// THE DEFECT.
//
// UpdateItem was a bare UPDATE:
//
//	UPDATE cart_items SET quantity = LEAST($3, 99)
//	WHERE cart_id = $1 AND variant_id = $2
//
// PUT /cart/items is an ABSOLUTE SET -- the client supplies the quantity it wants,
// without first knowing whether the line exists. When it did not, the UPDATE matched
// zero rows, pgx returned no error, and the handler answered 200 with an unchanged
// cart. Checkout deletes every line in the cart while leaving it active, so "the
// line is absent" is the ordinary state right after a purchase, not an edge case.
//
// This is a STRUCTURAL guard: it reads the source and asserts the statement upserts.
// It cannot prove a row was written -- that needs a database, which this repository
// cannot start locally (PostgreSQL fails with error 487). The behavioural witness is
// the `cart-stepper` e2e spec, which is the test that found this.
func TestUpdateItemUpsertsWhenTheLineIsAbsent(t *testing.T) {
	body := updateItemBody(t)

	if strings.Contains(body, "UPDATE cart_items SET quantity") {
		t.Fatal("UpdateItem is a bare UPDATE again: a PUT for an absent line will " +
			"match zero rows and return success with an unchanged cart")
	}
	if !strings.Contains(body, "INSERT INTO cart_items") {
		t.Fatal("UpdateItem does not insert: PUT /cart/items cannot create a line")
	}
	if !strings.Contains(body, "ON CONFLICT (cart_id, variant_id)") {
		t.Fatal("UpdateItem inserts without ON CONFLICT: a PUT for an existing line " +
			"would violate the unique constraint instead of setting the quantity")
	}
}
