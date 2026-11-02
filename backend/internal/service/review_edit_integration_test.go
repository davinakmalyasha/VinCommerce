package service

import (
	"context"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// A buyer can correct or remove their own review, and nobody else's. The
// ownership check lives in the row guard (`AND user_id = $2`): zero rows is
// the same answer for a missing id and somebody else's, so the endpoint is
// not an ownership oracle. An edit returns the review to pending moderation --
// approved content swapped without review would bypass moderation.
func TestBuyerCanEditAndDeleteOnlyTheirOwnReview(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var productID, buyerID, strangerID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM products WHERE status = 'active' LIMIT 1`,
	).Scan(&productID); err != nil {
		t.Fatalf("no active product to review: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'buyer.sample@vincommerce.com'`,
	).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'seller.elektro@vincommerce.com'`,
	).Scan(&strangerID); err != nil {
		t.Fatalf("seeded seller missing: %v", err)
	}

	var reviewID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, title, content, status)
		VALUES ($1, $2, 5, 'integration', 'original content', 'approved')
		RETURNING id::text`, productID, buyerID).Scan(&reviewID); err != nil {
		t.Fatalf("insert review: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM product_reviews WHERE id = $1`, reviewID)
	})

	reviews := repository.NewReviewRepository(pool)

	// A stranger's edit and delete both fail, and the row survives both.
	if err := reviews.UpdateOwnReview(ctx, reviewID, strangerID, 1, "", "vandalised", nil); err == nil {
		t.Fatal("a stranger edited somebody else's review")
	}
	if err := reviews.DeleteOwnReview(ctx, reviewID, strangerID); err == nil {
		t.Fatal("a stranger deleted somebody else's review")
	}
	var content string
	if err := pool.QueryRow(ctx,
		`SELECT content FROM product_reviews WHERE id = $1`, reviewID).Scan(&content); err != nil {
		t.Fatalf("the review did not survive the stranger's attempts: %v", err)
	}
	if content != "original content" {
		t.Fatalf("the review content changed to %q", content)
	}

	// Invalid edits fail even for the owner: the edit path must not accept
	// what the create path refuses.
	if err := reviews.UpdateOwnReview(ctx, reviewID, buyerID, 0, "", "bad rating", nil); err == nil {
		t.Fatal("an invalid rating was accepted on edit")
	}
	if err := reviews.UpdateOwnReview(ctx, reviewID, buyerID, 4, "", "   ", nil); err == nil {
		t.Fatal("empty text was accepted on edit")
	}

	// The owner's edit lands and returns the review to pending moderation.
	if err := reviews.UpdateOwnReview(ctx, reviewID, buyerID, 4, "integration", "corrected content", nil); err != nil {
		t.Fatalf("owner edit: %v", err)
	}
	var status string
	var rating int
	if err := pool.QueryRow(ctx,
		`SELECT status, rating FROM product_reviews WHERE id = $1`, reviewID,
	).Scan(&status, &rating); err != nil {
		t.Fatalf("read edited review: %v", err)
	}
	if status != "pending" {
		t.Fatalf("edited review status = %q, want pending (re-moderation)", status)
	}
	if rating != 4 {
		t.Fatalf("edited review rating = %d, want 4", rating)
	}
	if err := pool.QueryRow(ctx,
		`SELECT content FROM product_reviews WHERE id = $1`, reviewID).Scan(&content); err != nil {
		t.Fatalf("read edited content: %v", err)
	}
	if content != "corrected content" {
		t.Fatalf("edited content = %q", content)
	}

	// The owner's own listing still shows the pending review: an edit must not
	// make it vanish from their view.
	mine, err := reviews.ListByUser(ctx, buyerID, 50)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	found := false
	for _, r := range mine {
		if r.ID == reviewID {
			found = true
			if r.Status != "pending" {
				t.Fatalf("own listing shows status %q, want pending", r.Status)
			}
		}
	}
	if !found {
		t.Fatal("the edited review is missing from the author's own listing")
	}

	// And the stranger sees nothing of it.
	theirs, err := reviews.ListByUser(ctx, strangerID, 50)
	if err != nil {
		t.Fatalf("ListByUser(stranger): %v", err)
	}
	for _, r := range theirs {
		if r.ID == reviewID {
			t.Fatal("somebody else's review appears in the stranger's listing")
		}
	}

	// The owner's delete removes it.
	if err := reviews.DeleteOwnReview(ctx, reviewID, buyerID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_reviews WHERE id = $1`, reviewID).Scan(&remaining); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if remaining != 0 {
		t.Fatal("the review survived its owner's delete")
	}
}
