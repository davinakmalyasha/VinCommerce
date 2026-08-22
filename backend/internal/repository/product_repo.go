package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// ProductRepository persists products, variants, images and reviews.
type ProductRepository struct {
	pool *db.Pool
}

// NewProductRepository creates a ProductRepository.
func NewProductRepository(pool *db.Pool) *ProductRepository {
	return &ProductRepository{pool: pool}
}

// Pool exposes the underlying pool for seeding and ad-hoc queries.
func (r *ProductRepository) Pool() *db.Pool { return r.pool }

// Search runs the faceted product query.
func (r *ProductRepository) Search(ctx context.Context, f *domain.ProductFilter) (*domain.SearchResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 60 {
		f.PageSize = 24
	}

	whereSQL, args := r.buildSearchWhere(f)

	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM products p WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	// typo-tolerant fallback: exact FTS found nothing → retry with pg_trgm similarity
	if total == 0 && f.Query != "" {
		f.Fuzzy = true
		whereSQL, args = r.buildSearchWhere(f)
		if err := r.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM products p WHERE `+whereSQL, args...).Scan(&total); err != nil {
			return nil, err
		}
	}

	orderBy := r.buildSearchOrder(f)

	offset := (f.Page - 1) * f.PageSize
	args = append(args, f.PageSize, offset)

	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.avg_rating, p.rating_count, p.sold_count,
		       p.published_at, p.created_at, p.updated_at,
		       COALESCE(c.name, ''), COALESCE(b.name, ''),
		       (SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       (SELECT MAX(v.compare_at_price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       (SELECT v.image_url FROM product_variants v WHERE v.product_id = p.id AND v.is_active AND v.image_url IS NOT NULL LIMIT 1),
		       (SELECT string_agg(i.url, ',' ORDER BY i.position) FROM product_images i WHERE i.product_id = p.id)
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN brands b ON b.id = p.brand_id
		WHERE `+whereSQL+`
		ORDER BY `+orderBy+`
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)),
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var attrs []byte
		var cap *float64
		var catName, brandName string
		var minPrice *float64
		var imageURL *string
		var imageCSV *string
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt,
			&catName, &brandName, &minPrice, &cap, &imageURL, &imageCSV); err != nil {
			return nil, err
		}
		_ = cap
		if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
			return nil, err
		}
		if catName != "" {
			p.Category = &domain.Category{Name: catName}
		}
		if brandName != "" {
			p.Brand = &domain.Brand{Name: brandName}
		}
		if minPrice != nil {
			p.Variants = []*domain.ProductVariant{{Price: *minPrice}}
		}
		if imageURL != nil && *imageURL != "" {
			p.Images = []*domain.ProductImage{{URL: *imageURL, IsPrimary: true}}
		}
		if imageCSV != nil && *imageCSV != "" {
			for _, u := range strings.Split(*imageCSV, ",") {
				p.Images = append(p.Images, &domain.ProductImage{URL: u})
			}
		}
		items = append(items, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &domain.SearchResult{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}, nil
}

// ByID fetches a product with variants, images, category and brand.
func (r *ProductRepository) ByID(ctx context.Context, id string) (*domain.Product, error) {
	p, err := r.byID(ctx, `WHERE p.id = $1 AND p.status = 'active'`, id)
	if err != nil {
		return nil, err
	}
	return r.loadDetail(ctx, p)
}

// BySlug fetches an active product by slug.
func (r *ProductRepository) BySlug(ctx context.Context, slug string) (*domain.Product, error) {
	p, err := r.byID(ctx, `WHERE p.slug = $1 AND p.status = 'active'`, slug)
	if err != nil {
		return nil, err
	}
	return r.loadDetail(ctx, p)
}

// ByIDAnyStatus fetches a product regardless of status (seller/admin use).
func (r *ProductRepository) ByIDAnyStatus(ctx context.Context, id string) (*domain.Product, error) {
	p, err := r.byID(ctx, `WHERE p.id = $1`, id)
	if err != nil {
		return nil, err
	}
	return r.loadDetail(ctx, p)
}

func (r *ProductRepository) byID(ctx context.Context, where string, arg any) (*domain.Product, error) {
	var p domain.Product
	var attrs []byte
	var seoTitle, seoDesc *string
	err := r.pool.QueryRow(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.seo_title, p.seo_description,
		       p.avg_rating, p.rating_count, p.sold_count, p.published_at, p.created_at, p.updated_at
		FROM products p `+where, arg).
		Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &seoTitle, &seoDesc,
			&p.AvgRating, &p.RatingCount, &p.SoldCount, &p.PublishedAt, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if seoTitle != nil {
		p.SeoTitle = *seoTitle
	}
	if seoDesc != nil {
		p.SeoDescription = *seoDesc
	}
	if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProductRepository) loadDetail(ctx context.Context, p *domain.Product) (*domain.Product, error) {
	if p.CategoryID != nil {
		var c domain.Category
		if err := r.pool.QueryRow(ctx,
			`SELECT id, parent_id, name, slug, path, depth, position, is_active FROM categories WHERE id = $1`,
			*p.CategoryID).
			Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &c.Depth, &c.Position, &c.IsActive); err == nil {
			p.Category = &c
		}
	}
	if p.BrandID != nil {
		var b domain.Brand
		if err := r.pool.QueryRow(ctx,
			`SELECT id, name, slug, COALESCE(logo_url,''), is_active FROM brands WHERE id = $1`, *p.BrandID).
			Scan(&b.ID, &b.Name, &b.Slug, &b.LogoURL, &b.IsActive); err == nil {
			p.Brand = &b
		}
	}
	var sellerName string
	if err := r.pool.QueryRow(ctx,
		`SELECT full_name FROM users WHERE id = $1`, p.SellerID).Scan(&sellerName); err == nil {
		p.Seller = &domain.StoreSummary{ID: p.SellerID, Name: sellerName}
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, product_id, sku, name, price, compare_at_price, stock, weight_grams,
		       COALESCE(image_url,''), attributes, is_active
		FROM product_variants WHERE product_id = $1 ORDER BY created_at`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.ProductVariant
		var attrs []byte
		var cap *float64
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.Name, &v.Price, &cap, &v.Stock, &v.WeightGrams,
			&v.ImageURL, &attrs, &v.IsActive); err != nil {
			return nil, err
		}
		v.CompareAtPrice = cap
		if err := json.Unmarshal(attrs, &v.Attributes); err != nil {
			return nil, err
		}
		p.Variants = append(p.Variants, &v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	imgs, err := r.pool.Query(ctx, `
		SELECT id, product_id, url, position, is_primary
		FROM product_images WHERE product_id = $1 ORDER BY position`, p.ID)
	if err != nil {
		return nil, err
	}
	defer imgs.Close()
	for imgs.Next() {
		var img domain.ProductImage
		if err := imgs.Scan(&img.ID, &img.ProductID, &img.URL, &img.Position, &img.IsPrimary); err != nil {
			return nil, err
		}
		p.Images = append(p.Images, &img)
	}
	return p, imgs.Err()
}

// Related returns products sharing the category, excluding the given id.
func (r *ProductRepository) Related(ctx context.Context, productID string, limit int) ([]*domain.Product, error) {
	if limit < 1 || limit > 20 {
		limit = 8
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.avg_rating, p.rating_count, p.sold_count,
		       p.published_at, p.created_at, p.updated_at,
		       COALESCE(c.name, ''), COALESCE(b.name, ''),
		       (SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       NULL, NULL, NULL
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN brands b ON b.id = p.brand_id
		WHERE p.status = 'active'
		  AND p.id <> $1
		  AND p.category_id IN (SELECT category_id FROM products WHERE id = $1)
		ORDER BY p.sold_count DESC
		LIMIT $2`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var attrs []byte
		var cap *float64
		var catName, brandName string
		var minPrice *float64
		var imageURL *string
		var imageCSV *string
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt,
			&catName, &brandName, &minPrice, &cap, &imageURL, &imageCSV); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
			return nil, err
		}
		if catName != "" {
			p.Category = &domain.Category{Name: catName}
		}
		if brandName != "" {
			p.Brand = &domain.Brand{Name: brandName}
		}
		if minPrice != nil {
			p.Variants = []*domain.ProductVariant{{Price: *minPrice}}
		}
		if imageCSV != nil && *imageCSV != "" {
			p.Images = []*domain.ProductImage{{URL: strings.Split(*imageCSV, ",")[0], IsPrimary: true}}
		}
		items = append(items, &p)
	}
	return items, rows.Err()
}

// CreateWithVariants inserts a product with its variants and images in one transaction.
func (r *ProductRepository) CreateWithVariants(ctx context.Context, p *domain.Product, variants []*domain.ProductVariant, images []*domain.ProductImage) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if p.Attributes == nil {
		p.Attributes = map[string]string{}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO products (id, seller_id, category_id, brand_id, name, slug, description, status, attributes)
		VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9)`,
		p.ID, p.SellerID, p.CategoryID, p.BrandID, p.Name, p.Slug, p.Description, p.Status, p.Attributes); err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "product slug is taken"), nil)
	}

	for _, v := range variants {
		if v.Attributes == nil {
			v.Attributes = map[string]string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_variants (id, product_id, sku, name, price, compare_at_price, stock, weight_grams, image_url, attributes, is_active)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), $7, $8, NULLIF($9, ''), $10, $11)`,
			v.ID, p.ID, v.SKU, v.Name, v.Price, v.CompareAtPrice, v.Stock, v.WeightGrams, v.ImageURL, v.Attributes, v.IsActive); err != nil {
			return err
		}
	}

	for i, img := range images {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_images (id, product_id, url, position, is_primary)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
			p.ID, img.URL, i, i == 0); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// UpdateWithVariants updates a product, upserting variants by SKU and soft-deactivating removed ones.
// Variants are never hard-deleted so order history (stock ledger, reservations) stays intact.
func (r *ProductRepository) UpdateWithVariants(ctx context.Context, p *domain.Product, variants []*domain.ProductVariant, images []*domain.ProductImage) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE products SET category_id = NULLIF($2, '')::uuid, brand_id = NULLIF($3, '')::uuid,
			name = $4, slug = $5, description = $6, attributes = $7, updated_at = now()
		WHERE id = $1`,
		p.ID, p.CategoryID, p.BrandID, p.Name, p.Slug, p.Description, p.Attributes); err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "product slug is taken"), nil)
	}

	// Soft-deactivate all variants not present in the new set (by SKU).
	if _, err := tx.Exec(ctx, `
		UPDATE product_variants SET is_active = FALSE, updated_at = now()
		WHERE product_id = $1 AND NOT (sku = ANY($2::text[]))`,
		p.ID, skusOf(variants)); err != nil {
		return err
	}

	for _, v := range variants {
		if v.Attributes == nil {
			v.Attributes = map[string]string{}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE product_variants SET name = $3, price = $4, compare_at_price = NULLIF($5, 0),
				stock = $6, weight_grams = $7, image_url = NULLIF($8, ''), attributes = $9,
				is_active = TRUE, updated_at = now()
			WHERE product_id = $1 AND sku = $2`,
			p.ID, v.SKU, v.Name, v.Price, v.CompareAtPrice, v.Stock, v.WeightGrams, v.ImageURL, v.Attributes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO product_variants (id, product_id, sku, name, price, compare_at_price, stock, weight_grams, image_url, attributes, is_active)
				VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), $7, $8, NULLIF($9, ''), $10, TRUE)`,
				uuid.NewString(), p.ID, v.SKU, v.Name, v.Price, v.CompareAtPrice, v.Stock, v.WeightGrams, v.ImageURL, v.Attributes); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1`, p.ID); err != nil {
		return err
	}
	for i, img := range images {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_images (id, product_id, url, position, is_primary)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
			p.ID, img.URL, i, i == 0); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func skusOf(variants []*domain.ProductVariant) []string {
	out := make([]string, 0, len(variants))
	for _, v := range variants {
		out = append(out, v.SKU)
	}
	return out
}

// ListBySeller returns a seller's products with variant counts.
func (r *ProductRepository) ListBySeller(ctx context.Context, sellerID string, page, pageSize int) ([]*domain.Product, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM products WHERE seller_id = $1`, sellerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.avg_rating, p.rating_count, p.sold_count,
		       p.published_at, p.created_at, p.updated_at,
		       COALESCE((SELECT SUM(v.stock) FROM product_variants v WHERE v.product_id = p.id), 0),
		       (SELECT COUNT(*) FROM product_variants v WHERE v.product_id = p.id)
		FROM products p
		WHERE p.seller_id = $1
		ORDER BY p.created_at DESC
		LIMIT $2 OFFSET $3`, sellerID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var attrs []byte
		var stockSum int
		var variantCount int
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt, &stockSum, &variantCount); err != nil {
			return nil, 0, err
		}
		_ = stockSum
		_ = variantCount
		if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
			return nil, 0, err
		}
		items = append(items, &p)
	}
	return items, total, rows.Err()
}

// SlugEntry is a product URL candidate for the sitemap.
type SlugEntry struct {
	Slug      string    `json:"slug"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LowStockVariant is a variant at/below the stock threshold.
type LowStockVariant struct {
	VariantID   string `json:"variant_id"`
	SellerID    string `json:"seller_id"`
	ProductName string `json:"product_name"`
	VariantName string `json:"variant_name"`
	Stock       int    `json:"stock"`
}

// LowStockVariants lists active variants at/below the threshold that have not
// been alerted since `since` (worker).
func (r *ProductRepository) LowStockVariants(ctx context.Context, threshold int, since time.Time, limit int) ([]*LowStockVariant, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT v.id, p.seller_id, p.name, v.name, v.stock
		FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE v.is_active AND p.status = 'active' AND v.stock <= $1
		  AND NOT EXISTS (SELECT 1 FROM low_stock_alerts a WHERE a.variant_id = v.id AND a.sent_at > $2)
		ORDER BY v.stock
		LIMIT $3`, threshold, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*LowStockVariant{}
	for rows.Next() {
		var v LowStockVariant
		if err := rows.Scan(&v.VariantID, &v.SellerID, &v.ProductName, &v.VariantName, &v.Stock); err != nil {
			return nil, err
		}
		items = append(items, &v)
	}
	return items, rows.Err()
}

// LowStockForSeller lists the seller's variants at/below the threshold.
func (r *ProductRepository) LowStockForSeller(ctx context.Context, sellerID string, threshold int) ([]*LowStockVariant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT v.id, p.seller_id, p.name, v.name, v.stock
		FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE v.is_active AND p.status = 'active' AND p.seller_id = $1 AND v.stock <= $2
		ORDER BY v.stock
		LIMIT 20`, sellerID, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*LowStockVariant{}
	for rows.Next() {
		var v LowStockVariant
		if err := rows.Scan(&v.VariantID, &v.SellerID, &v.ProductName, &v.VariantName, &v.Stock); err != nil {
			return nil, err
		}
		items = append(items, &v)
	}
	return items, rows.Err()
}

// UpsertLowStockAlert records that the seller was alerted (dedupe window).
func (r *ProductRepository) UpsertLowStockAlert(ctx context.Context, sellerID, variantID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO low_stock_alerts (seller_id, variant_id) VALUES ($1, $2)
		ON CONFLICT (seller_id, variant_id) DO UPDATE SET sent_at = now()`,
		sellerID, variantID)
	return err
}

// ClearLowStockAlert removes the alert when stock recovers.
func (r *ProductRepository) ClearLowStockAlert(ctx context.Context, variantID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM low_stock_alerts WHERE variant_id = $1`, variantID)
	return err
}

// VariantStock returns the current stock of a variant.
func (r *ProductRepository) VariantStock(ctx context.Context, variantID string) (int, error) {
	var stock int
	err := r.pool.QueryRow(ctx,
		`SELECT stock FROM product_variants WHERE id = $1`, variantID).Scan(&stock)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return stock, err
}

// AllActiveSlugs lists slugs of publishable products (sitemap).
func (r *ProductRepository) AllActiveSlugs(ctx context.Context) ([]*SlugEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT slug, updated_at FROM products WHERE status = 'active' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*SlugEntry{}
	for rows.Next() {
		var e SlugEntry
		if err := rows.Scan(&e.Slug, &e.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &e)
	}
	return items, rows.Err()
}

// SetStatus updates a product's status and publication timestamp.
func (r *ProductRepository) SetStatus(ctx context.Context, productID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE products SET status = $2::varchar, updated_at = now(),
			published_at = CASE WHEN $2::varchar = 'active' THEN now() ELSE published_at END
		WHERE id = $1`, productID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ProductReport is a buyer's moderation report with product context.
type ProductReport struct {
	ID           string     `json:"id"`
	ProductID    string     `json:"product_id"`
	ProductName  string     `json:"product_name"`
	ProductSlug  string     `json:"product_slug"`
	ReporterID   string     `json:"reporter_id"`
	ReporterName string     `json:"reporter_name"`
	SellerID     string     `json:"seller_id"`
	Reason       string     `json:"reason"`
	Description  string     `json:"description"`
	Status       string     `json:"status"`
	AdminNote    string     `json:"admin_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// CreateReport inserts a product report.
func (r *ProductRepository) CreateReport(ctx context.Context, id, productID, userID, reason, description string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO product_reports (id, product_id, user_id, reason, description)
		VALUES ($1, $2, $3, $4, $5)`, id, productID, userID, reason, description)
	return err
}

// ReportByID fetches one report (any status).
func (r *ProductRepository) ReportByID(ctx context.Context, reportID string) (*ProductReport, error) {
	var rep ProductReport
	err := r.pool.QueryRow(ctx, reportSelect+` WHERE r.id = $1`, reportID).
		Scan(&rep.ID, &rep.ProductID, &rep.ProductName, &rep.ProductSlug, &rep.ReporterID, &rep.ReporterName,
			&rep.SellerID, &rep.Reason, &rep.Description, &rep.Status, &rep.AdminNote, &rep.CreatedAt, &rep.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rep, nil
}

const reportSelect = `
	SELECT r.id, r.product_id, p.name, p.slug, r.user_id, u.full_name, p.seller_id,
	       r.reason, r.description, r.status, COALESCE(r.admin_note, ''), r.created_at, r.resolved_at
	FROM product_reports r
	JOIN products p ON p.id = r.product_id
	JOIN users u ON u.id = r.user_id`

// ListReports lists reports, optionally filtered by status (admin).
func (r *ProductRepository) ListReports(ctx context.Context, status string) ([]*ProductReport, error) {
	query := reportSelect
	args := []any{}
	if status != "" && status != "all" {
		query += ` WHERE r.status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY r.created_at DESC LIMIT 100`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*ProductReport{}
	for rows.Next() {
		var rep ProductReport
		if err := rows.Scan(&rep.ID, &rep.ProductID, &rep.ProductName, &rep.ProductSlug, &rep.ReporterID, &rep.ReporterName,
			&rep.SellerID, &rep.Reason, &rep.Description, &rep.Status, &rep.AdminNote, &rep.CreatedAt, &rep.ResolvedAt); err != nil {
			return nil, err
		}
		items = append(items, &rep)
	}
	return items, rows.Err()
}

// ResolveReport closes a report with an admin note.
func (r *ProductRepository) ResolveReport(ctx context.Context, reportID, note string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE product_reports SET status = 'resolved', admin_note = NULLIF($2, ''), resolved_at = now()
		WHERE id = $1 AND status = 'open'`, reportID, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Bestsellers returns top-selling products (recommendation baseline).
func (r *ProductRepository) Bestsellers(ctx context.Context, limit int) ([]*domain.Product, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.brand_id, p.name, p.slug, p.description,
		       p.status, p.attributes, p.avg_rating, p.rating_count, p.sold_count,
		       p.published_at, p.created_at, p.updated_at,
		       COALESCE(c.name, ''), COALESCE(b.name, ''),
		       (SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       NULL, NULL,
		       (SELECT string_agg(i.url, ',' ORDER BY i.position) FROM product_images i WHERE i.product_id = p.id)
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN brands b ON b.id = p.brand_id
		WHERE p.status = 'active'
		ORDER BY p.sold_count DESC, p.avg_rating DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var attrs []byte
		var cap *float64
		var catName, brandName string
		var minPrice *float64
		var imageURL *string
		var imageCSV *string
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.Name, &p.Slug, &p.Description,
			&p.Status, &attrs, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt,
			&catName, &brandName, &minPrice, &cap, &imageURL, &imageCSV); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attrs, &p.Attributes); err != nil {
			return nil, err
		}
		if minPrice != nil {
			p.Variants = []*domain.ProductVariant{{Price: *minPrice}}
		}
		if imageCSV != nil && *imageCSV != "" {
			p.Images = []*domain.ProductImage{{URL: strings.Split(*imageCSV, ",")[0], IsPrimary: true}}
		}
		items = append(items, &p)
	}
	return items, rows.Err()
}

// ProductSuggestions returns name+slug pairs matching a prefix (autocomplete).
func (r *ProductRepository) ProductSuggestions(ctx context.Context, prefix string, limit int) ([]*domain.NameSlug, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT name, slug FROM products
		WHERE status = 'active' AND name ILIKE $1 || '%'
		ORDER BY sold_count DESC
		LIMIT $2`, prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.NameSlug{}
	for rows.Next() {
		var s domain.NameSlug
		if err := rows.Scan(&s.Name, &s.Slug); err != nil {
			return nil, err
		}
		items = append(items, &s)
	}
	return items, rows.Err()
}

// VariantOwner resolves the seller of a variant.
func (r *ProductRepository) VariantOwner(ctx context.Context, variantID string, owner *string) error {
	err := r.pool.QueryRow(ctx, `
		SELECT p.seller_id FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE v.id = $1`, variantID).Scan(owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// FacetBrands returns brand counts for the current filter set.
func (r *ProductRepository) FacetBrands(ctx context.Context, whereSQL string, args []any) ([]*domain.FacetCount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.name, COUNT(*)::bigint
		FROM products p
		JOIN brands b ON b.id = p.brand_id
		WHERE `+whereSQL+`
		GROUP BY b.id, b.name
		ORDER BY COUNT(*) DESC, b.name
		LIMIT 12`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facets := []*domain.FacetCount{}
	for rows.Next() {
		var f domain.FacetCount
		if err := rows.Scan(&f.ID, &f.Label, &f.Count); err != nil {
			return nil, err
		}
		facets = append(facets, &f)
	}
	return facets, rows.Err()
}

func safeIdent(s string) string {
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, "\\", "")
	return s
}

// buildSearchWhere assembles the filter clauses and their bound arguments.
func (r *ProductRepository) buildSearchWhere(f *domain.ProductFilter) (string, []any) {
	where := []string{"p.status = 'active'"}
	args := []any{}

	if f.Query != "" {
		args = append(args, f.Query)
		if f.Fuzzy {
			where = append(where, fmt.Sprintf("similarity(p.name, $%d) > 0.2", len(args)))
		} else {
			where = append(where, fmt.Sprintf("p.search_vector @@ websearch_to_tsquery('english', $%d)", len(args)))
		}
	}
	if f.CategoryID != "" {
		args = append(args, f.CategoryID)
		n := len(args)
		where = append(where, fmt.Sprintf(
			"p.category_id IN (SELECT id FROM categories WHERE id = $%d OR path LIKE (SELECT path FROM categories WHERE id = $%d) || '/%%')",
			n, n))
	}
	if f.CategorySlug != "" {
		args = append(args, f.CategorySlug)
		n := len(args)
		where = append(where, fmt.Sprintf(
			"p.category_id IN (SELECT id FROM categories WHERE slug = $%d OR path LIKE (SELECT path FROM categories WHERE slug = $%d) || '/%%')",
			n, n))
	}
	if len(f.BrandIDs) > 0 {
		args = append(args, f.BrandIDs)
		where = append(where, fmt.Sprintf("p.brand_id = ANY($%d)", len(args)))
	}
	if f.MinPrice != nil {
		args = append(args, *f.MinPrice)
		where = append(where, fmt.Sprintf(
			"p.id IN (SELECT DISTINCT product_id FROM product_variants WHERE is_active AND price >= $%d)", len(args)))
	}
	if f.MaxPrice != nil {
		args = append(args, *f.MaxPrice)
		where = append(where, fmt.Sprintf(
			"p.id IN (SELECT DISTINCT product_id FROM product_variants WHERE is_active AND price <= $%d)", len(args)))
	}
	if f.Rating != nil {
		args = append(args, *f.Rating)
		where = append(where, fmt.Sprintf("p.avg_rating >= $%d", len(args)))
	}
	for slug, values := range f.AttrFilters {
		if len(values) == 0 {
			continue
		}
		args = append(args, values)
		where = append(where, fmt.Sprintf("p.attributes->>'%s' = ANY($%d)", safeIdent(slug), len(args)))
	}
	return strings.Join(where, " AND "), args
}

// buildSearchOrder picks the ORDER BY clause; args must match buildSearchWhere's output.
func (r *ProductRepository) buildSearchOrder(f *domain.ProductFilter) string {
	orderBy := "p.avg_rating DESC, p.rating_count DESC"
	if f.Query != "" {
		if f.Fuzzy {
			orderBy = "similarity(p.name, $1) DESC, p.avg_rating DESC"
		} else {
			orderBy = "ts_rank(p.search_vector, websearch_to_tsquery('english', $1)) DESC, p.avg_rating DESC"
		}
	}
	switch f.Sort {
	case "price_asc":
		orderBy = "(SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active) ASC"
	case "price_desc":
		orderBy = "(SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active) DESC"
	case "newest":
		orderBy = "p.created_at DESC"
	case "bestseller":
		orderBy = "p.sold_count DESC"
	case "rating":
		orderBy = "p.avg_rating DESC, p.rating_count DESC"
	}
	return orderBy
}
