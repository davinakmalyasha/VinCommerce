package service

import (
	"context"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// Seller replies were written to review_replies and shown in the seller inbox,
// but the buyer-facing product listing never joined them: a shopper could not
// see that the store had responded. This asserts the reply travels from the
// table to the listing, and that a review without one carries no reply.
func TestProductListingCarriesSellerReplies(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var productID, buyerID, sellerID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.seller_id::text
		  FROM products p WHERE p.status = 'active' LIMIT 1`,
	).Scan(&productID, &sellerID); err != nil {
		t.Fatalf("no active product to review: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'buyer.sample@vincommerce.com'`,
	).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}

	var withReply, withoutReply string
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, title, content, status)
		VALUES ($1, $2, 5, 'integration', 'reply travels to listing', 'approved')
		RETURNING id::text`, productID, buyerID).Scan(&withReply); err != nil {
		t.Fatalf("insert review: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, title, content, status)
		VALUES ($1, $2, 4, 'integration', 'no reply here', 'approved')
		RETURNING id::text`, productID, buyerID).Scan(&withoutReply); err != nil {
		t.Fatalf("insert second review: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM product_reviews WHERE id = $1 OR id = $2`, withReply, withoutReply)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO review_replies (review_id, user_id, content)
		VALUES ($1, $2, 'Terima kasih atas ulasannya!')`, withReply, sellerID); err != nil {
		t.Fatalf("insert reply: %v", err)
	}

	reviewsRepo := repository.NewReviewRepository(pool)
	reviews, _, err := reviewsRepo.ListByProduct(ctx, productID, 1, 50, "helpful")
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	byID := map[string]string{}
	for _, r := range reviews {
		byID[r.ID] = r.Reply
	}
	if got, ok := byID[withReply]; !ok {
		t.Fatalf("the replied review is missing from the listing")
	} else if got != "Terima kasih atas ulasannya!" {
		t.Fatalf("listing reply = %q, want the seller response", got)
	}
	if got, ok := byID[withoutReply]; !ok {
		t.Fatalf("the unreplied review is missing from the listing")
	} else if got != "" {
		t.Fatalf("unreplied review carries reply %q", got)
	}
}
