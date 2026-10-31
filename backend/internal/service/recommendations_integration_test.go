package service

import (
	"context"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// The recommendation rail ranked global bestsellers for everyone, including a
// logged-in buyer whose own order history is the strongest signal available.
// The SQL orders bought categories first and falls back to sales/rating; this
// asserts the fallback did not swallow the personalisation.
func TestRecommendationsPreferCategoriesTheBuyerBought(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var buyerID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'buyer.sample@vincommerce.com'`).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT DISTINCT p.category_id::text
		  FROM orders o
		  JOIN order_items oi ON oi.order_id = o.id
		  JOIN products p ON p.id = oi.product_id
		 WHERE o.buyer_id = $1::uuid
		   AND o.status IN ('paid', 'packed', 'shipped', 'partially_shipped', 'delivered', 'completed')
		   AND p.category_id IS NOT NULL`, buyerID)
	if err != nil {
		t.Fatalf("read bought categories: %v", err)
	}
	bought := map[string]bool{}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			t.Fatalf("scan category: %v", err)
		}
		bought[category] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate categories: %v", err)
	}
	if len(bought) == 0 {
		t.Skip("the seeded buyer has no completed orders to personalise from")
	}

	products := repository.NewProductRepository(pool)
	items, err := products.BestsellersForBuyer(ctx, buyerID, 10)
	if err != nil {
		t.Fatalf("BestsellersForBuyer: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("personalised recommendations returned nothing")
	}
	if len(items) > 10 {
		t.Fatalf("returned %d items for limit 10", len(items))
	}
	firstCategory := ""
	if items[0].CategoryID != nil {
		firstCategory = *items[0].CategoryID
	}
	if !bought[firstCategory] {
		t.Fatalf("the first recommendation is in category %q, which the buyer never ordered from; "+
			"the bought-category ordering is not being applied", firstCategory)
	}

	// The anonymous rail must be untouched by the personalisation change.
	global, err := products.Bestsellers(ctx, 10)
	if err != nil {
		t.Fatalf("Bestsellers: %v", err)
	}
	if len(global) == 0 {
		t.Fatal("the global bestseller rail is empty")
	}
}
