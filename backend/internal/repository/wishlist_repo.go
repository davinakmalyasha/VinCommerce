package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// WishlistRepository persists wishlists and flash sales.
type WishlistRepository struct {
	pool *db.Pool
}

// NewWishlistRepository creates a WishlistRepository.
func NewWishlistRepository(pool *db.Pool) *WishlistRepository {
	return &WishlistRepository{pool: pool}
}

// Add inserts a wishlist entry (idempotent).
func (r *WishlistRepository) Add(ctx context.Context, userID, variantID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO wishlists (user_id, variant_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, variantID)
	return err
}

// Remove deletes a wishlist entry.
func (r *WishlistRepository) Remove(ctx context.Context, userID, variantID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM wishlists WHERE user_id = $1 AND variant_id = $2`, userID, variantID)
	return err
}

// List returns the user's wishlist with product info.
func (r *WishlistRepository) List(ctx context.Context, userID string, page, pageSize int) ([]*domain.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM wishlists WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.avg_rating, p.rating_count, p.sold_count,
		       p.published_at, p.created_at, p.updated_at,
		       COALESCE(c.name, ''), COALESCE(b.name, ''),
		       (SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       (SELECT string_agg(i.url, ',' ORDER BY i.position) FROM product_images i WHERE i.product_id = p.id),
		       w.variant_id
		FROM wishlists w
		JOIN products p ON p.id = (SELECT v.product_id FROM product_variants v WHERE v.id = w.variant_id)
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN brands b ON b.id = p.brand_id
		WHERE w.user_id = $1
		ORDER BY w.created_at DESC
		LIMIT $2 OFFSET $3`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var attrs []byte
		var cap *float64
		var catName, brandName string
		var minPrice *float64
		var imageCSV *string
		var variantID string
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt,
			&catName, &brandName, &minPrice, &imageCSV, &variantID); err != nil {
			return nil, 0, err
		}
		_ = cap
		if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
			return nil, 0, err
		}
		if minPrice != nil {
			p.Variants = []*domain.ProductVariant{{ID: variantID, Price: *minPrice}}
		}
		if imageCSV != nil && *imageCSV != "" {
			p.Images = []*domain.ProductImage{{URL: strings.Split(*imageCSV, ",")[0], IsPrimary: true}}
		}
		items = append(items, &p)
	}
	return items, total, rows.Err()
}

// ActiveFlashSale returns the currently running flash sale.
func (r *WishlistRepository) ActiveFlashSale(ctx context.Context) (*domain.FlashSale, error) {
	var f domain.FlashSale
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(description,''), starts_at, ends_at, is_active
		FROM flash_sales
		WHERE is_active AND starts_at <= now() AND ends_at > now()
		ORDER BY starts_at DESC LIMIT 1`).
		Scan(&f.ID, &f.Name, &f.Description, &f.StartsAt, &f.EndsAt, &f.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_FLASH_SALE", "no active flash sale")
	}
	return &f, err
}

// FlashSaleItems returns the sale's items with product data.
func (r *WishlistRepository) FlashSaleItems(ctx context.Context, saleID string) ([]*domain.FlashSaleItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT fi.id, fi.flash_sale_id, fi.variant_id, fi.sale_price, fi.initial_stock, fi.sold_count,
		       v.price, v.stock, v.name, v.sku, COALESCE(v.image_url, ''),
		       p.id, p.name, p.slug
		FROM flash_sale_items fi
		JOIN product_variants v ON v.id = fi.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE fi.flash_sale_id = $1
		ORDER BY fi.sold_count DESC`, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.FlashSaleItem{}
	for rows.Next() {
		var it domain.FlashSaleItem
		if err := rows.Scan(&it.ID, &it.FlashSaleID, &it.VariantID, &it.SalePrice, &it.InitialStock, &it.SoldCount,
			&it.RegularPrice, &it.Stock, &it.VariantName, &it.SKU, &it.ImageURL,
			&it.ProductID, &it.ProductName, &it.ProductSlug); err != nil {
			return nil, err
		}
		items = append(items, &it)
	}
	return items, rows.Err()
}

// FlashSalePriceFor returns the active flash sale price for a variant, if any.
func (r *WishlistRepository) FlashSalePriceFor(ctx context.Context, variantID string) (*float64, error) {
	var price float64
	err := r.pool.QueryRow(ctx, `
		SELECT fi.sale_price
		FROM flash_sale_items fi
		JOIN flash_sales f ON f.id = fi.flash_sale_id
		WHERE fi.variant_id = $1 AND f.is_active AND f.starts_at <= now() AND f.ends_at > now()
		LIMIT 1`, variantID).Scan(&price)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &price, nil
}

var _ = time.Second
