package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// CartRepository persists carts, cart items and addresses.
type CartRepository struct {
	pool *db.Pool
}

// NewCartRepository creates a CartRepository.
func NewCartRepository(pool *db.Pool) *CartRepository {
	return &CartRepository{pool: pool}
}

// EnsureActiveByUser returns (creating if needed) the user's active cart.
func (r *CartRepository) EnsureActiveByUser(ctx context.Context, userID string) (*domain.Cart, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO carts (id, user_id, status)
		VALUES (gen_random_uuid(), $1, 'active')
		ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
		RETURNING id`, userID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &domain.Cart{ID: id, UserID: &userID, Status: domain.CartActive}, nil
}

// EnsureActiveBySession returns (creating if needed) the guest cart.
func (r *CartRepository) EnsureActiveBySession(ctx context.Context, sessionKey string) (*domain.Cart, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO carts (id, session_key, status)
		VALUES (gen_random_uuid(), $1, 'active')
		ON CONFLICT (session_key) DO UPDATE SET updated_at = now()
		RETURNING id`, sessionKey).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &domain.Cart{ID: id, SessionKey: sessionKey, Status: domain.CartActive}, nil
}

// GetWithItems loads a cart with lines.
func (r *CartRepository) GetWithItems(ctx context.Context, cartID string) (*domain.Cart, []*domain.CartLine, error) {
	var cart domain.Cart
	var userID, sessionKey *string
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, session_key, status FROM carts WHERE id = $1`, cartID).
		Scan(&cart.ID, &userID, &sessionKey, &cart.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	cart.UserID = userID
	if sessionKey != nil {
		cart.SessionKey = *sessionKey
	}

	rows, err := r.pool.Query(ctx, `
		SELECT ci.id, ci.variant_id, ci.quantity,
		       v.product_id, p.name, v.name, v.sku, COALESCE(v.image_url, ''),
		       v.price, v.price * ci.quantity, v.stock,
		       p.seller_id, u.full_name, v.weight_grams, v.is_active
		FROM cart_items ci
		JOIN product_variants v ON v.id = ci.variant_id
		JOIN products p ON p.id = v.product_id
		JOIN users u ON u.id = p.seller_id
		WHERE ci.cart_id = $1
		ORDER BY ci.created_at`, cartID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	lines := []*domain.CartLine{}
	for rows.Next() {
		var l domain.CartLine
		if err := rows.Scan(&l.ID, &l.VariantID, &l.Quantity,
			&l.ProductID, &l.ProductName, &l.VariantName, &l.SKU, &l.ImageURL,
			&l.Price, &l.Subtotal, &l.Stock,
			&l.SellerID, &l.SellerName, &l.WeightGrams, &l.IsActive); err != nil {
			return nil, nil, err
		}
		lines = append(lines, &l)
	}
	return &cart, lines, rows.Err()
}

// EnsureVariant loads stock/active state for a variant.
func (r *CartRepository) EnsureVariant(ctx context.Context, variantID string, stock *int, active *bool) error {
	err := r.pool.QueryRow(ctx,
		`SELECT stock, is_active FROM product_variants WHERE id = $1`, variantID).
		Scan(stock, active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.E(domain.KindNotFound, "VARIANT_NOT_FOUND", "variant not found")
	}
	return err
}

// AddItem inserts or increments a cart line.
func (r *CartRepository) AddItem(ctx context.Context, cartID, variantID string, quantity int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO cart_items (id, cart_id, variant_id, quantity)
		VALUES (gen_random_uuid(), $1, $2, $3)
		ON CONFLICT (cart_id, variant_id)
		DO UPDATE SET quantity = LEAST(cart_items.quantity + EXCLUDED.quantity, 99)`,
		cartID, variantID, quantity)
	return err
}

// UpdateItem sets a cart line quantity.
func (r *CartRepository) UpdateItem(ctx context.Context, cartID, variantID string, quantity int) error {
	if quantity <= 0 {
		return r.RemoveItem(ctx, cartID, variantID)
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE cart_items SET quantity = LEAST($3, 99)
		WHERE cart_id = $1 AND variant_id = $2`,
		cartID, variantID, quantity)
	return err
}

// RemoveItem deletes a cart line.
func (r *CartRepository) RemoveItem(ctx context.Context, cartID, variantID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2`, cartID, variantID)
	return err
}

// RemoveItems deletes multiple cart lines in one statement.
func (r *CartRepository) RemoveItems(ctx context.Context, cartID string, variantIDs []string) error {
	if len(variantIDs) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = ANY($2::uuid[])`,
		cartID, variantIDs)
	return err
}

// MoveToWishlist inserts the lines into the user's wishlist and removes them
// from the cart in one transaction.
func (r *CartRepository) MoveToWishlist(ctx context.Context, userID, cartID string, variantIDs []string) error {
	if len(variantIDs) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO wishlists (user_id, variant_id)
		SELECT $1, ci.variant_id FROM cart_items ci
		WHERE ci.cart_id = $2 AND ci.variant_id = ANY($3::uuid[])
		ON CONFLICT DO NOTHING`, userID, cartID, variantIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = ANY($2::uuid[])`,
		cartID, variantIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Clear empties the cart.
func (r *CartRepository) Clear(ctx context.Context, cartID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cartID)
	return err
}

// Merge moves guest cart items into the user cart, marking the guest cart merged.
func (r *CartRepository) Merge(ctx context.Context, guestCartID, userCartID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO cart_items (id, cart_id, variant_id, quantity)
		SELECT gen_random_uuid(), $2, variant_id, quantity
		FROM cart_items
		WHERE cart_id = $1
		ON CONFLICT (cart_id, variant_id)
		DO UPDATE SET quantity = LEAST(cart_items.quantity + EXCLUDED.quantity, 99)`,
		guestCartID, userCartID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE carts SET status = 'merged' WHERE id = $1`, guestCartID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CartForUser returns the user's active cart id.
func (r *CartRepository) CartForUser(ctx context.Context, userID string) (string, bool, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM carts WHERE user_id = $1 AND status = 'active'`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// CartForSession returns the guest cart id.
func (r *CartRepository) CartForSession(ctx context.Context, sessionKey string) (string, bool, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM carts WHERE session_key = $1 AND status = 'active'`, sessionKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// AbandonedCart is a stale cart eligible for recovery outreach.
type AbandonedCart struct {
	ID        string
	UserID    string
	Email     string
	FullName  string
	ItemCount int
	ItemNames string
	UpdatedAt time.Time
}

// AbandonedCarts lists user carts idle past the threshold that still hold
// items and have not yet received a recovery email.
func (r *CartRepository) AbandonedCarts(ctx context.Context, olderThan time.Time, limit int) ([]*AbandonedCart, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.user_id, u.email, u.full_name,
		       (SELECT COUNT(*) FROM cart_items ci WHERE ci.cart_id = c.id),
		       COALESCE((SELECT string_agg(p.name, ', ' ORDER BY ci.created_at) FROM cart_items ci
		                 JOIN product_variants v ON v.id = ci.variant_id
		                 JOIN products p ON p.id = v.product_id
		                 WHERE ci.cart_id = c.id LIMIT 3), ''),
		       c.updated_at
		FROM carts c
		JOIN users u ON u.id = c.user_id
		WHERE c.status = 'active'
		  AND c.user_id IS NOT NULL
		  AND c.recovery_sent_at IS NULL
		  AND c.updated_at < $1
		  AND EXISTS (SELECT 1 FROM cart_items ci WHERE ci.cart_id = c.id)
		ORDER BY c.updated_at
		LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*AbandonedCart{}
	for rows.Next() {
		var a AbandonedCart
		if err := rows.Scan(&a.ID, &a.UserID, &a.Email, &a.FullName, &a.ItemCount, &a.ItemNames, &a.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}

// MarkRecovered stamps carts as contacted so they are not re-emailed.
func (r *CartRepository) MarkRecovered(ctx context.Context, cartIDs []string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE carts SET recovery_sent_at = now()
		WHERE id = ANY($1::uuid[])`, cartIDs)
	return err
}
