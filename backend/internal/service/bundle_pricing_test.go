package service

import (
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

func cartLine(sellerID, variantID string, qty int, subtotal float64) *domain.CartLine {
	return &domain.CartLine{
		CartItem: domain.CartItem{VariantID: variantID, Quantity: qty},
		SellerID: sellerID,
		Subtotal: subtotal,
	}
}

func bundle(sellerID string, price float64, items ...domain.BundleItem) *domain.Bundle {
	return &domain.Bundle{SellerID: sellerID, Price: price, IsActive: true, Items: items}
}

func items(pairs ...any) []domain.BundleItem {
	out := []domain.BundleItem{}
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, domain.BundleItem{VariantID: pairs[i].(string), Quantity: pairs[i+1].(int)})
	}
	return out
}

func cartOf(lines ...*domain.CartLine) (map[string]map[string]int, map[string]float64) {
	bySeller := map[string]map[string]int{}
	subtotals := map[string]float64{}
	for _, l := range lines {
		m := bySeller[l.SellerID]
		if m == nil {
			m = map[string]int{}
			bySeller[l.SellerID] = m
		}
		m[l.VariantID] += l.Quantity
		subtotals[l.SellerID] += l.Subtotal
	}
	return bySeller, subtotals
}

// The rule this file exists to protect: a bundle is a FIXED SET, not a threshold.
func TestMatchBundles(t *testing.T) {
	lines := []*domain.CartLine{
		cartLine("seller-a", "v1", 1, 60000),
		cartLine("seller-a", "v2", 2, 100000),
	}
	bySeller, subtotals := cartOf(lines...)

	t.Run("exact match yields subtotal minus the bundle price", func(t *testing.T) {
		b := bundle("seller-a", 140000, items("v1", 1, "v2", 2)...)
		got := matchBundles(bySeller, []*domain.Bundle{b}, subtotals)
		if m, ok := got["seller-a"]; !ok {
			t.Fatal("exact match did not apply")
		} else if m.discount != 20000 {
			t.Fatalf("discount = %v, want 20000", m.discount)
		}
	})

	t.Run("an extra item from the same seller disables the bundle", func(t *testing.T) {
		extra := append(append([]*domain.CartLine{}, lines...), cartLine("seller-a", "v3", 1, 5000))
		bs, sub := cartOf(extra...)
		b := bundle("seller-a", 140000, items("v1", 1, "v2", 2)...)
		if got := matchBundles(bs, []*domain.Bundle{b}, sub); len(got) != 0 {
			t.Fatalf("bundle applied to a cart with an extra line: %+v", got)
		}
	})

	t.Run("a different quantity does not match", func(t *testing.T) {
		b := bundle("seller-a", 140000, items("v1", 1, "v2", 3)...)
		if got := matchBundles(bySeller, []*domain.Bundle{b}, subtotals); len(got) != 0 {
			t.Fatalf("bundle applied at the wrong quantity: %+v", got)
		}
	})

	t.Run("a bundle for another seller does not match", func(t *testing.T) {
		b := bundle("seller-b", 140000, items("v1", 1, "v2", 2)...)
		if got := matchBundles(bySeller, []*domain.Bundle{b}, subtotals); len(got) != 0 {
			t.Fatalf("a cross-seller bundle applied: %+v", got)
		}
	})

	t.Run("a bundle that is not cheaper is ignored", func(t *testing.T) {
		b := bundle("seller-a", 160000, items("v1", 1, "v2", 2)...) // subtotal is 160000
		if got := matchBundles(bySeller, []*domain.Bundle{b}, subtotals); len(got) != 0 {
			t.Fatalf("a bundle at or above the subtotal applied: %+v", got)
		}
	})

	t.Run("the deepest matching bundle wins", func(t *testing.T) {
		shallow := bundle("seller-a", 150000, items("v1", 1, "v2", 2)...)
		deep := bundle("seller-a", 120000, items("v1", 1, "v2", 2)...)
		got := matchBundles(bySeller, []*domain.Bundle{shallow, deep}, subtotals)
		if m := got["seller-a"]; m.discount != 40000 {
			t.Fatalf("discount = %v, want the deeper 40000", m.discount)
		}
	})
}
