package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// QARepository persists product questions & answers.
type QARepository struct {
	pool *db.Pool
}

// NewQARepository creates a QARepository.
func NewQARepository(pool *db.Pool) *QARepository {
	return &QARepository{pool: pool}
}

// Ask inserts a buyer question.
func (r *QARepository) Ask(ctx context.Context, id, productID, userID, question string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO product_qa (id, product_id, user_id, question) VALUES ($1, $2, $3, $4)`,
		id, productID, userID, question)
	return err
}

// Answer records a seller answer.
func (r *QARepository) Answer(ctx context.Context, qaID, sellerID, answer string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE product_qa SET answer = $2, answered_by = $3, answered_at = now() WHERE id = $1`,
		qaID, answer, sellerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// QAByProduct lists questions for a product (answered first).
func (r *QARepository) QAByProduct(ctx context.Context, productID string, limit int) ([]*domain.ProductQA, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT q.id, q.product_id, q.user_id, q.question, COALESCE(q.answer,''),
		       q.answered_at, q.created_at, u.full_name
		FROM product_qa q
		JOIN users u ON u.id = q.user_id
		WHERE q.product_id = $1
		ORDER BY (q.answer IS NOT NULL), q.created_at DESC
		LIMIT $2`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.ProductQA{}
	for rows.Next() {
		var q domain.ProductQA
		if err := rows.Scan(&q.ID, &q.ProductID, &q.UserID, &q.Question, &q.Answer,
			&q.AnsweredAt, &q.CreatedAt, &q.AskUserName); err != nil {
			return nil, err
		}
		items = append(items, &q)
	}
	return items, rows.Err()
}

// QAByID loads a question.
func (r *QARepository) QAByID(ctx context.Context, qaID string) (*domain.ProductQA, error) {
	var q domain.ProductQA
	err := r.pool.QueryRow(ctx, `
		SELECT q.id, q.product_id, q.user_id, q.question, COALESCE(q.answer,''),
		       q.answered_at, q.created_at, u.full_name
		FROM product_qa q
		JOIN users u ON u.id = q.user_id
		WHERE q.id = $1`, qaID).
		Scan(&q.ID, &q.ProductID, &q.UserID, &q.Question, &q.Answer,
			&q.AnsweredAt, &q.CreatedAt, &q.AskUserName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &q, err
}

// QASeller verifies a user sells the product of a question.
func (r *QARepository) QASeller(ctx context.Context, qaID, sellerID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM product_qa q JOIN products p ON p.id = q.product_id
			WHERE q.id = $1 AND p.seller_id = $2)`, qaID, sellerID).Scan(&ok)
	return ok, err
}

// --- bundles ---

// CreateBundle inserts a bundle with items.
func (r *QARepository) CreateBundle(ctx context.Context, b *domain.Bundle, items []domain.BundleItem) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO bundles (id, seller_id, name, description, price, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		b.ID, b.SellerID, b.Name, b.Description, b.Price, b.IsActive); err != nil {
		return err
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO bundle_items (bundle_id, variant_id, quantity) VALUES ($1, $2, $3)`,
			b.ID, it.VariantID, it.Quantity); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Bundles lists active bundles (optionally for a seller).
func (r *QARepository) Bundles(ctx context.Context, sellerID string) ([]*domain.Bundle, error) {
	where := `b.is_active = TRUE`
	args := []any{}
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND b.seller_id = $1`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.seller_id, b.name, b.description, b.price, b.is_active, b.created_at,
		       COALESCE(json_agg(json_build_object('variant_id', bi.variant_id, 'quantity', bi.quantity)) FILTER (WHERE bi.variant_id IS NOT NULL), '[]')
		FROM bundles b
		LEFT JOIN bundle_items bi ON bi.bundle_id = b.id
		WHERE `+where+`
		GROUP BY b.id ORDER BY b.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Bundle{}
	for rows.Next() {
		var b domain.Bundle
		var itemsJSON []byte
		if err := rows.Scan(&b.ID, &b.SellerID, &b.Name, &b.Description, &b.Price, &b.IsActive, &b.CreatedAt, &itemsJSON); err != nil {
			return nil, err
		}
		if len(itemsJSON) > 0 {
			_ = json.Unmarshal(itemsJSON, &b.Items)
		}
		items = append(items, &b)
	}
	return items, rows.Err()
}

// BundleByID loads a bundle with items.
func (r *QARepository) BundleByID(ctx context.Context, bundleID string) (*domain.Bundle, error) {
	var b domain.Bundle
	err := r.pool.QueryRow(ctx, `
		SELECT b.id, b.seller_id, b.name, b.description, b.price, b.is_active, b.created_at
		FROM bundles b WHERE b.id = $1`, bundleID).
		Scan(&b.ID, &b.SellerID, &b.Name, &b.Description, &b.Price, &b.IsActive, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT variant_id, quantity FROM bundle_items WHERE bundle_id = $1`, bundleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it domain.BundleItem
		if err := rows.Scan(&it.VariantID, &it.Quantity); err != nil {
			return nil, err
		}
		b.Items = append(b.Items, it)
	}
	return &b, rows.Err()
}

// --- price alerts ---

// CreatePriceAlert registers a target-price watch.
func (r *QARepository) CreatePriceAlert(ctx context.Context, a *domain.PriceAlert) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO price_alerts (id, user_id, variant_id, target_price) VALUES ($1, $2, $3, $4)`,
		a.ID, a.UserID, a.VariantID, a.TargetPrice)
	return err
}

// PriceAlerts lists the user's active alerts with current prices.
func (r *QARepository) PriceAlerts(ctx context.Context, userID string) ([]*domain.PriceAlert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.variant_id, a.target_price, a.status, a.created_at,
		       v.price, p.name, p.slug, v.name
		FROM price_alerts a
		JOIN product_variants v ON v.id = a.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE a.user_id = $1 AND a.status = 'active'
		ORDER BY a.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.PriceAlert{}
	for rows.Next() {
		var a domain.PriceAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.VariantID, &a.TargetPrice, &a.Status, &a.CreatedAt,
			&a.CurrentPrice, &a.ProductName, &a.ProductSlug, &a.VariantName); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}

// CancelPriceAlert deactivates an alert (owner).
func (r *QARepository) CancelPriceAlert(ctx context.Context, alertID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE price_alerts SET status = 'cancelled' WHERE id = $1 AND user_id = $2`, alertID, userID)
	return err
}

// TriggeredAlerts lists alerts whose target is met (worker).
func (r *QARepository) TriggeredAlerts(ctx context.Context, limit int) ([]*domain.PriceAlert, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.variant_id, a.target_price, a.status, a.created_at,
		       v.price, p.name, p.slug, v.name
		FROM price_alerts a
		JOIN product_variants v ON v.id = a.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE a.status = 'active' AND v.price <= a.target_price
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.PriceAlert{}
	for rows.Next() {
		var a domain.PriceAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.VariantID, &a.TargetPrice, &a.Status, &a.CreatedAt,
			&a.CurrentPrice, &a.ProductName, &a.ProductSlug, &a.VariantName); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}

// MarkAlertsTriggered flips alerts to triggered.
func (r *QARepository) MarkAlertsTriggered(ctx context.Context, ids []string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE price_alerts SET status = 'triggered' WHERE id = ANY($1::uuid[])`, ids)
	return err
}

// --- back-in-stock alerts ---

// CreateBackInStock registers a restock watch (dedupe per user+variant).
func (r *QARepository) CreateBackInStock(ctx context.Context, a *domain.BackInStockAlert) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO back_in_stock_alerts (id, user_id, variant_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		a.ID, a.UserID, a.VariantID).Scan(&a.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.E(domain.KindConflict, "ALREADY_WATCHING", "you are already watching this variant")
	}
	return err
}

// BackInStockAlerts lists the user's active restock watches.
func (r *QARepository) BackInStockAlerts(ctx context.Context, userID string) ([]*domain.BackInStockAlert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.variant_id, a.status, a.created_at, a.triggered_at,
		       p.name, p.slug, v.name
		FROM back_in_stock_alerts a
		JOIN product_variants v ON v.id = a.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE a.user_id = $1 AND a.status = 'active'
		ORDER BY a.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.BackInStockAlert{}
	for rows.Next() {
		var a domain.BackInStockAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.VariantID, &a.Status, &a.CreatedAt, &a.TriggeredAt,
			&a.ProductName, &a.ProductSlug, &a.VariantName); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}

// CancelBackInStock deactivates a watch (owner).
func (r *QARepository) CancelBackInStock(ctx context.Context, alertID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE back_in_stock_alerts SET status = 'cancelled' WHERE id = $1 AND user_id = $2`, alertID, userID)
	return err
}

// RestockedAlerts lists active watches whose variant now has stock (worker).
func (r *QARepository) RestockedAlerts(ctx context.Context, limit int) ([]*domain.BackInStockAlert, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.variant_id, a.status, a.created_at, a.triggered_at,
		       p.name, p.slug, v.name
		FROM back_in_stock_alerts a
		JOIN product_variants v ON v.id = a.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE a.status = 'active' AND v.stock > 0 AND v.is_active
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.BackInStockAlert{}
	for rows.Next() {
		var a domain.BackInStockAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.VariantID, &a.Status, &a.CreatedAt, &a.TriggeredAt,
			&a.ProductName, &a.ProductSlug, &a.VariantName); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}

// MarkBackInStockTriggered flips restock watches to triggered.
func (r *QARepository) MarkBackInStockTriggered(ctx context.Context, ids []string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE back_in_stock_alerts SET status = 'triggered', triggered_at = now()
		WHERE id = ANY($1::uuid[])`, ids)
	return err
}

// --- flash sale admin ---

// CreateFlashSale inserts a sale (admin).
func (r *QARepository) CreateFlashSale(ctx context.Context, name, description string, startsAt, endsAt time.Time) (*domain.FlashSale, error) {
	f := &domain.FlashSale{ID: uuid.NewString(), Name: name, Description: description, StartsAt: startsAt, EndsAt: endsAt, IsActive: true}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO flash_sales (id, name, description, starts_at, ends_at, is_active)
		VALUES ($1, $2, $3, $4, $5, TRUE) RETURNING id`,
		f.ID, f.Name, f.Description, f.StartsAt, f.EndsAt).Scan(&f.ID)
	return f, err
}

// AddFlashSaleItems adds variants to a sale (admin).
func (r *QARepository) AddFlashSaleItems(ctx context.Context, saleID string, items []domain.FlashSaleItem) error {
	for _, it := range items {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO flash_sale_items (id, flash_sale_id, variant_id, sale_price, initial_stock)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)
			ON CONFLICT (flash_sale_id, variant_id) DO UPDATE SET sale_price = EXCLUDED.sale_price`,
			saleID, it.VariantID, it.SalePrice, it.InitialStock)
		if err != nil {
			return err
		}
	}
	return nil
}

// ListFlashSales lists all sales (admin).
func (r *QARepository) ListFlashSales(ctx context.Context) ([]*domain.FlashSale, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, COALESCE(description,''), starts_at, ends_at, is_active
		FROM flash_sales ORDER BY starts_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.FlashSale{}
	for rows.Next() {
		var f domain.FlashSale
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.StartsAt, &f.EndsAt, &f.IsActive); err != nil {
			return nil, err
		}
		items = append(items, &f)
	}
	return items, rows.Err()
}

// SetFlashSaleActive toggles a sale (admin).
func (r *QARepository) SetFlashSaleActive(ctx context.Context, saleID string, active bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE flash_sales SET is_active = $2 WHERE id = $1`, saleID, active)
	return err
}

// ReturnOrderSeller resolves the order and seller of a return request.
func (r *QARepository) ReturnOrderSeller(ctx context.Context, returnID string, orderID, sellerID *string) error {
	err := r.pool.QueryRow(ctx, `
		SELECT order_id, seller_id FROM return_requests WHERE id = $1`, returnID).
		Scan(orderID, sellerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
