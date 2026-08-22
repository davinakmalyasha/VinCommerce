package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// CatalogService implements category, brand and attribute queries.
type CatalogService struct {
	categories *repository.CategoryRepository
	cache      *cache.Store
}

// NewCatalogService creates a CatalogService.
func NewCatalogService(categories *repository.CategoryRepository) *CatalogService {
	return &CatalogService{categories: categories}
}

// SetCache enables hot-path caching (5 min TTL).
func (s *CatalogService) SetCache(c *cache.Store) { s.cache = c }

// Tree returns the full category tree.
func (s *CatalogService) Tree(ctx context.Context) ([]*domain.Category, error) {
	if s.cache != nil {
		var cached []*domain.Category
		if ok, err := s.cache.Get(ctx, "cache:categories", &cached); err == nil && ok {
			return cached, nil
		}
	}
	flat, err := s.categories.Tree(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*domain.Category{}
	roots := []*domain.Category{}
	for _, c := range flat {
		byID[c.ID] = c
		if c.ParentID == nil {
			roots = append(roots, c)
		}
	}
	for _, c := range flat {
		if c.ParentID != nil {
			if parent, ok := byID[*c.ParentID]; ok {
				parent.Children = append(parent.Children, c)
			}
		}
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, "cache:categories", roots, 5*time.Minute)
	}
	return roots, nil
}

// CategoryBySlug fetches a category with its subtree slugs.
func (s *CatalogService) CategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	return s.categories.BySlug(ctx, slug)
}

// Brands lists active brands.
func (s *CatalogService) Brands(ctx context.Context) ([]*domain.Brand, error) {
	return s.categories.Brands(ctx)
}

// Attributes lists filterable attributes with values.
func (s *CatalogService) Attributes(ctx context.Context) ([]*domain.Attribute, error) {
	return s.categories.Attributes(ctx)
}

// CreateCategory adds a category node (admin).
func (s *CatalogService) CreateCategory(ctx context.Context, parentID *string, name string, position int) (*domain.Category, error) {
	slug := slugify(name)
	c := &domain.Category{
		ID:       uuid.NewString(),
		ParentID: parentID,
		Name:     name,
		Slug:     slug,
		Position: position,
	}
	if parentID != nil {
		parent, err := s.categories.ByID(ctx, *parentID)
		if err != nil {
			return nil, err
		}
		c.Path = parent.Path + "/" + slug
		c.Depth = parent.Depth + 1
	} else {
		c.Path = slug
	}
	if err := s.categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CreateBrand (admin).
func (s *CatalogService) CreateBrand(ctx context.Context, name, logoURL string) (*domain.Brand, error) {
	b := &domain.Brand{ID: uuid.NewString(), Name: name, Slug: slugify(name), LogoURL: logoURL, IsActive: true}
	if err := s.categories.CreateBrand(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// SetBrandActive (admin).
func (s *CatalogService) SetBrandActive(ctx context.Context, brandID string, active bool) error {
	return s.categories.SetBrandActive(ctx, brandID, active)
}

// CreateAttribute (admin).
func (s *CatalogService) CreateAttribute(ctx context.Context, a *domain.Attribute) (*domain.Attribute, error) {
	if strings.TrimSpace(a.Name) == "" {
		return nil, domain.E(domain.KindInvalid, "NAME_REQUIRED", "attribute name is required")
	}
	a.ID = uuid.NewString()
	a.Slug = slugify(a.Name)
	for _, v := range a.Values {
		v.Slug = slugify(v.Value)
	}
	if err := s.categories.CreateAttribute(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "'", "")
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else {
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fmt.Sprintf("item-%s", uuid.NewString()[:8])
	}
	return out
}
