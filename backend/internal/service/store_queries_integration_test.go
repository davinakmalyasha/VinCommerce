package service

import (
	"context"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// The two most recent SQL defects, asserted against a real database.
//
// Both were invisible to every test that does not have one:
//   - the public store page 500'd because ListActiveBySeller selected 17 columns and
//     scanned 15 -- pgx fails that only when a row exists, so a missing slug looked
//     correct (404) and a real store did not (500);
//   - the seller product list rendered Rp 0 / 0 stok / "0 varian" because the query
//     computed the aggregates and discarded them.
//
// Neither can be caught by reading the source, which is why they survived long enough
// to be found by an e2e suite and a screenshot.
func TestStoreQueriesReturnRealData(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	stores := repository.NewStoreRepository(pool)

	// 1. The public store page: a real store must resolve WITH its products.
	store, err := stores.BySlug(ctx, "elektrostore")
	if err != nil {
		t.Fatalf("BySlug(elektrostore): %v -- the seeded store slug is the seller name", err)
	}
	if store.ID == "" || store.Status != "active" {
		t.Fatalf("seeded store is not usable: id=%q status=%q", store.ID, store.Status)
	}

	orders := repository.NewOrderRepository(pool)
	sellerSvc := NewSellerService(
		stores,
		repository.NewUserRepository(pool),
		repository.NewProductRepository(pool),
		orders,
		repository.NewPaymentRepository(pool),
	)
	public, products, err := sellerSvc.PublicStore(ctx, "elektrostore", "")
	if err != nil {
		// This is the exact shape of the 500: BySlug succeeds and the scan or the
		// product listing fails.
		t.Fatalf("PublicStore(elektrostore): %v", err)
	}
	if public == nil || len(products) == 0 {
		t.Fatalf("the seeded store has no public products (store=%v products=%d)",
			public != nil, len(products))
	}

	// 2. The seller listing: the aggregates the table renders must be populated.
	var sellerID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = 'seller.elektro@vincommerce.com'`).Scan(&sellerID); err != nil {
		t.Fatalf("seeded seller missing: %v", err)
	}
	listed, total, err := sellerSvc.ListSellerProducts(ctx, sellerID, 1, 50)
	if err != nil {
		t.Fatalf("ListSellerProducts: %v", err)
	}
	if total == 0 || len(listed) == 0 {
		t.Fatalf("the seeded seller has no products (total=%d)", total)
	}
	withStock := 0
	for _, p := range listed {
		if p.Stock > 0 {
			withStock++
			if p.VariantCount <= 0 {
				t.Errorf("%s: stock %d but variant_count %d", p.Name, p.Stock, p.VariantCount)
			}
			if p.MinPrice <= 0 {
				t.Errorf("%s: stock %d but min_price %v", p.Name, p.Stock, p.MinPrice)
			}
		}
	}
	if withStock == 0 {
		t.Fatalf("no seeded product reports stock > 0 -- the seller table can only " +
			"render zeroes, which is exactly what it did before")
	}
}
