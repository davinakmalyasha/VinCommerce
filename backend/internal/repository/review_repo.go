package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// ReviewRepository persists product reviews.
type ReviewRepository struct {
	pool *db.Pool
}

// NewReviewRepository creates a ReviewRepository.
func NewReviewRepository(pool *db.Pool) *ReviewRepository {
	return &ReviewRepository{pool: pool}
}

// Create inserts a review (or returns conflict if the user already reviewed the order item).
func (r *ReviewRepository) Create(ctx context.Context, rev *domain.ProductReview) error {
	if rev.Images == nil {
		rev.Images = []string{}
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO product_reviews (id, product_id, user_id, order_item_id, rating, title, content, images, status)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9)
		RETURNING created_at`,
		rev.ID, rev.ProductID, rev.UserID, rev.OrderItemID, rev.Rating, rev.Title, rev.Content, rev.Images, rev.Status).
		Scan(&rev.CreatedAt)
	if err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "REVIEW_EXISTS", "review already exists"), nil)
	}
	return nil
}

// ProductSeller resolves the seller of a review's product.
func (r *ReviewRepository) ProductSeller(ctx context.Context, reviewID string, sellerID *string) error {
	err := r.pool.QueryRow(ctx, `
		SELECT p.seller_id FROM product_reviews rv
		JOIN products p ON p.id = rv.product_id
		WHERE rv.id = $1`, reviewID).Scan(sellerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// Reply stores a seller reply to a review.
func (r *ReviewRepository) Reply(ctx context.Context, reviewID, userID, content string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO review_replies (id, review_id, user_id, content)
		VALUES (gen_random_uuid(), $1, $2, $3)
		ON CONFLICT (review_id) DO UPDATE SET content = EXCLUDED.content`,
		reviewID, userID, content)
	return err
}

// RatingDistribution returns the star histogram for approved reviews.
func (r *ReviewRepository) RatingDistribution(ctx context.Context, productID string) ([]*domain.RatingCount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT rating, COUNT(*)::int
		FROM product_reviews
		WHERE product_id = $1 AND status = 'approved'
		GROUP BY rating ORDER BY rating DESC`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.RatingCount{}
	for rows.Next() {
		var rc domain.RatingCount
		if err := rows.Scan(&rc.Rating, &rc.Count); err != nil {
			return nil, err
		}
		items = append(items, &rc)
	}
	return items, rows.Err()
}

// ListByProduct returns approved reviews for a product.
func (r *ReviewRepository) ListByProduct(ctx context.Context, productID string, page, pageSize int) ([]*domain.ProductReview, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}

	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM product_reviews WHERE product_id = $1 AND status = 'approved'`,
		productID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.product_id, r.user_id, r.order_item_id, r.rating, COALESCE(r.title,''), r.content,
		       r.images, r.status, r.helpful_count, r.created_at,
		       u.full_name
		FROM product_reviews r
		JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1 AND r.status = 'approved'
		ORDER BY r.helpful_count DESC, r.created_at DESC
		LIMIT $2 OFFSET $3`, productID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	reviews := []*domain.ProductReview{}
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return nil, 0, err
		}
		reviews = append(reviews, rev)
	}
	return reviews, total, rows.Err()
}

// ListAllApproved returns up to limit approved reviews (no pagination).
func (r *ReviewRepository) ListAllApproved(ctx context.Context, productID string, limit int) ([]*domain.ProductReview, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.product_id, r.user_id, r.order_item_id, r.rating, COALESCE(r.title,''), r.content,
		       r.images, r.status, r.helpful_count, r.created_at,
		       u.full_name
		FROM product_reviews r
		JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1 AND r.status = 'approved'
		ORDER BY r.created_at DESC
		LIMIT $2`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reviews := []*domain.ProductReview{}
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, rev)
	}
	return reviews, rows.Err()
}

// ToggleHelpful upserts a helpful vote and updates the counter.
func (r *ReviewRepository) ToggleHelpful(ctx context.Context, reviewID, userID string) (helpful bool, count int, err error) {
	var existing *bool
	err = r.pool.QueryRow(ctx,
		`SELECT helpful FROM review_helpful WHERE review_id = $1 AND user_id = $2`,
		reviewID, userID).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, 0, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}

	delta := 0
	if existing == nil {
		helpful = true
		delta = 1
	} else if *existing {
		helpful = false
		delta = -1
	} else {
		helpful = true
		delta = 1
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	if existing == nil {
		if _, err := tx.Exec(ctx,
			`INSERT INTO review_helpful (review_id, user_id, helpful) VALUES ($1, $2, TRUE)`,
			reviewID, userID); err != nil {
			return false, 0, err
		}
	} else if helpful {
		if _, err := tx.Exec(ctx,
			`UPDATE review_helpful SET helpful = TRUE WHERE review_id = $1 AND user_id = $2`,
			reviewID, userID); err != nil {
			return false, 0, err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`DELETE FROM review_helpful WHERE review_id = $1 AND user_id = $2`,
			reviewID, userID); err != nil {
			return false, 0, err
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE product_reviews SET helpful_count = GREATEST(helpful_count + $3, 0) WHERE id = $1`,
		reviewID, delta); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}

	if err := r.pool.QueryRow(ctx,
		`SELECT helpful_count FROM product_reviews WHERE id = $1`, reviewID).Scan(&count); err != nil {
		return false, 0, err
	}
	return helpful, count, nil
}

// UpdateStatus approves or rejects a review (admin).
func (r *ReviewRepository) UpdateStatus(ctx context.Context, reviewID, status, note string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE product_reviews SET status = $2, admin_note = NULLIF($3, ''), updated_at = now() WHERE id = $1`,
		reviewID, status, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Pending returns reviews awaiting moderation (admin).
func (r *ReviewRepository) Pending(ctx context.Context, limit int) ([]*domain.ProductReview, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.product_id, r.user_id, r.order_item_id, r.rating, COALESCE(r.title,''), r.content,
		       r.images, r.status, r.helpful_count, r.created_at,
		       u.full_name
		FROM product_reviews r
		JOIN users u ON u.id = r.user_id
		WHERE r.status = 'pending'
		ORDER BY r.created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reviews := []*domain.ProductReview{}
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, rev)
	}
	return reviews, rows.Err()
}

// SellerReview is an approved review on one of the seller's products.
type SellerReview struct {
	domain.ProductReview
	ProductName string `json:"product_name"`
	Reply       string `json:"reply,omitempty"`
}

// ListBySeller lists approved reviews across a seller's products (reviews inbox).
func (r *ReviewRepository) ListBySeller(ctx context.Context, sellerID string, limit int) ([]*SellerReview, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.product_id, r.user_id, r.order_item_id, r.rating, COALESCE(r.title,''), r.content,
		       r.images, r.status, r.helpful_count, r.created_at,
		       u.full_name, p.name,
		       COALESCE(rr.content,'')
		FROM product_reviews r
		JOIN products p ON p.id = r.product_id
		JOIN users u ON u.id = r.user_id
		LEFT JOIN review_replies rr ON rr.review_id = r.id
		WHERE p.seller_id = $1 AND r.status = 'approved'
		ORDER BY r.created_at DESC
		LIMIT $2`, sellerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reviews := []*SellerReview{}
	for rows.Next() {
		var sr SellerReview
		var images []byte
		err := rows.Scan(&sr.ID, &sr.ProductID, &sr.UserID, &sr.OrderItemID, &sr.Rating, &sr.Title,
			&sr.Content, &images, &sr.Status, &sr.HelpfulCount, &sr.CreatedAt,
			&sr.UserName, &sr.ProductName, &sr.Reply)
		if err != nil {
			return nil, err
		}
		if len(images) > 0 {
			_ = json.Unmarshal(images, &sr.Images)
		}
		reviews = append(reviews, &sr)
	}
	return reviews, rows.Err()
}

type reviewRow interface {
	Scan(dest ...any) error
}

func scanReview(row reviewRow) (*domain.ProductReview, error) {
	var rev domain.ProductReview
	var images []byte
	err := row.Scan(&rev.ID, &rev.ProductID, &rev.UserID, &rev.OrderItemID, &rev.Rating,
		&rev.Title, &rev.Content, &images, &rev.Status, &rev.HelpfulCount, &rev.CreatedAt,
		&rev.UserName)
	if err != nil {
		return nil, err
	}
	if len(images) > 0 {
		_ = json.Unmarshal(images, &rev.Images)
	}
	return &rev, nil
}
