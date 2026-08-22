package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// CategoryRepository persists the category tree.
type CategoryRepository struct {
	pool *db.Pool
}

// NewCategoryRepository creates a CategoryRepository.
func NewCategoryRepository(pool *db.Pool) *CategoryRepository {
	return &CategoryRepository{pool: pool}
}

// Create inserts a category, computing path and depth.
func (r *CategoryRepository) Create(ctx context.Context, c *domain.Category) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO categories (id, parent_id, name, slug, path, depth, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING path, depth`,
		c.ID, c.ParentID, c.Name, c.Slug, c.Path, c.Depth, c.Position).Scan(&c.Path, &c.Depth)
	if err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "category slug is taken"), nil)
	}
	return nil
}

// Tree returns all categories ordered for tree assembly.
func (r *CategoryRepository) Tree(ctx context.Context) ([]*domain.Category, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, parent_id, name, slug, path, depth, position, is_active
		FROM categories ORDER BY path, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cats := []*domain.Category{}
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &c.Depth, &c.Position, &c.IsActive); err != nil {
			return nil, err
		}
		cats = append(cats, &c)
	}
	return cats, rows.Err()
}

// BySlug finds a category by slug.
func (r *CategoryRepository) BySlug(ctx context.Context, slug string) (*domain.Category, error) {
	var c domain.Category
	err := r.pool.QueryRow(ctx, `
		SELECT id, parent_id, name, slug, path, depth, position, is_active
		FROM categories WHERE slug = $1`, slug).
		Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &c.Depth, &c.Position, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &c, err
}

// ByID finds a category by id.
func (r *CategoryRepository) ByID(ctx context.Context, id string) (*domain.Category, error) {
	var c domain.Category
	err := r.pool.QueryRow(ctx, `
		SELECT id, parent_id, name, slug, path, depth, position, is_active
		FROM categories WHERE id = $1`, id).
		Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &c.Depth, &c.Position, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &c, err
}

// CreateBrand adds a brand (admin).
func (r *CategoryRepository) CreateBrand(ctx context.Context, b *domain.Brand) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO brands (id, name, slug, logo_url, is_active)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)`,
		b.ID, b.Name, b.Slug, b.LogoURL, b.IsActive)
	return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "brand slug is taken"), nil)
}

// SetBrandActive toggles a brand (admin).
func (r *CategoryRepository) SetBrandActive(ctx context.Context, brandID string, active bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE brands SET is_active = $2 WHERE id = $1`, brandID, active)
	return err
}

// CreateAttribute adds an attribute with values (admin).
func (r *CategoryRepository) CreateAttribute(ctx context.Context, a *domain.Attribute) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var attrID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO attributes (id, name, slug, filterable, searchable, position)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		a.ID, a.Name, a.Slug, a.Filterable, a.Searchable, a.Position).Scan(&attrID); err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "attribute slug is taken"), nil)
	}
	for i, v := range a.Values {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attribute_values (id, attribute_id, value, slug, position)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)`, attrID, v.Value, v.Slug, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CategorySuggestions returns name+slug pairs matching a prefix (autocomplete).
func (r *CategoryRepository) CategorySuggestions(ctx context.Context, prefix string, limit int) ([]*domain.NameSlug, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT name, slug FROM categories
		WHERE is_active AND name ILIKE $1 || '%'
		ORDER BY depth, name
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

// BrandSuggestions returns name+slug pairs matching a prefix (autocomplete).
func (r *CategoryRepository) BrandSuggestions(ctx context.Context, prefix string, limit int) ([]*domain.NameSlug, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT name, slug FROM brands
		WHERE is_active AND name ILIKE $1 || '%'
		ORDER BY name
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

// Brands lists active brands.
func (r *CategoryRepository) Brands(ctx context.Context) ([]*domain.Brand, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, slug, COALESCE(logo_url,''), is_active
		FROM brands WHERE is_active ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := []*domain.Brand{}
	for rows.Next() {
		var b domain.Brand
		if err := rows.Scan(&b.ID, &b.Name, &b.Slug, &b.LogoURL, &b.IsActive); err != nil {
			return nil, err
		}
		brands = append(brands, &b)
	}
	return brands, rows.Err()
}

// Attributes lists attributes with their values.
func (r *CategoryRepository) Attributes(ctx context.Context) ([]*domain.Attribute, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.name, a.slug, a.filterable, a.searchable,
		       COALESCE(json_agg(json_build_object('id', v.id, 'value', v.value, 'slug', v.slug)
		              ORDER BY v.position) FILTER (WHERE v.id IS NOT NULL), '[]')
		FROM attributes a
		LEFT JOIN attribute_values v ON v.attribute_id = a.id
		WHERE a.is_active
		GROUP BY a.id
		ORDER BY a.position, a.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attrs := []*domain.Attribute{}
	for rows.Next() {
		var a domain.Attribute
		var valuesJSON []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.Filterable, &a.Searchable, &valuesJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(valuesJSON, &a.Values); err != nil {
			return nil, err
		}
		attrs = append(attrs, &a)
	}
	return attrs, rows.Err()
}
