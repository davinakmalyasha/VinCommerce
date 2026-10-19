package service

import (
	"os"
	"strings"
	"testing"
)

func updateCartItemBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("order_service.go")
	if err != nil {
		t.Fatalf("read order_service.go: %v", err)
	}
	src := string(raw)
	i := strings.Index(src, "func (s *OrderService) UpdateCartItem(")
	if i < 0 {
		t.Fatal("no UpdateCartItem")
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		return body[:end]
	}
	return body
}

// THE DEFECT.
//
// UpdateCartItem set the quantity without consulting stock or the variant's active
// flag; AddToCart checked both. A cautious client that always used PUT therefore
// had no guards at all -- quantity 99 against stock 3 was accepted, and checkout
// was left to notice. That was survivable while PUT only touched lines that had
// already been through POST; it stops being survivable the moment PUT can create
// the line, which is what the accompanying repository change makes it do.
//
// STRUCTURAL guard, like the repository one: it reads the source. A database-backed
// test cannot run here (PostgreSQL fails with error 487 on this machine), so this
// asserts the guards are present and lets the e2e suite and CI cover behaviour.
func TestUpdateCartItemAppliesTheSameGuardsAsAdd(t *testing.T) {
	body := updateCartItemBody(t)

	for _, want := range []struct {
		frag string
		why  string
	}{
		{"s.carts.EnsureVariant(", "no stock/active lookup: PUT can add an unavailable variant"},
		{"if !active {", "the active check is missing or inverted"},
		{`"VARIANT_INACTIVE"`, "inactive variants are not rejected"},
		{"if stock < quantity {", "the stock ceiling is not applied to the requested quantity"},
		{`"INSUFFICIENT_STOCK"`, "over-stock quantities are not reported"},
	} {
		if !strings.Contains(body, want.frag) {
			t.Fatalf("%s (missing %q)", want.why, want.frag)
		}
	}
}
