package service

import (
	"context"
	"fmt"
	"time"

	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// EngagementService implements wishlist, flash sales and recommendations.
type EngagementService struct {
	wishlist *repository.WishlistRepository
	products *repository.ProductRepository
	cache    *cache.Store
}

// NewEngagementService creates an EngagementService.
func NewEngagementService(wishlist *repository.WishlistRepository, products *repository.ProductRepository) *EngagementService {
	return &EngagementService{wishlist: wishlist, products: products}
}

// SetCache enables hot-path caching (5 min TTL).
func (s *EngagementService) SetCache(c *cache.Store) { s.cache = c }

// AddToWishlist adds a variant to the user's wishlist.
func (s *EngagementService) AddToWishlist(ctx context.Context, userID, variantID string) error {
	return s.wishlist.Add(ctx, userID, variantID)
}

// RemoveFromWishlist removes a variant.
func (s *EngagementService) RemoveFromWishlist(ctx context.Context, userID, variantID string) error {
	return s.wishlist.Remove(ctx, userID, variantID)
}

// Wishlist lists the user's saved products.
func (s *EngagementService) Wishlist(ctx context.Context, userID string, page, pageSize int) ([]*domain.Product, int64, error) {
	return s.wishlist.List(ctx, userID, page, pageSize)
}

// ActiveFlashSale returns the running sale.
func (s *EngagementService) ActiveFlashSale(ctx context.Context) (*domain.FlashSale, error) {
	return s.wishlist.ActiveFlashSale(ctx)
}

// FlashSaleItems lists discounted variants.
func (s *EngagementService) FlashSaleItems(ctx context.Context, saleID string) ([]*domain.FlashSaleItem, error) {
	return s.wishlist.FlashSaleItems(ctx, saleID)
}

// Recommended returns bestsellers as the baseline recommendation feed.
func (s *EngagementService) Recommended(ctx context.Context, limit int) ([]*domain.Product, error) {
	if limit < 1 || limit > 20 {
		limit = 10
	}
	key := fmt.Sprintf("cache:recommended:%d", limit)
	if s.cache != nil {
		var cached []*domain.Product
		if ok, err := s.cache.Get(ctx, key, &cached); err == nil && ok {
			return cached, nil
		}
	}
	items, err := s.products.Bestsellers(ctx, limit)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, key, items, 5*time.Minute)
	}
	return items, nil
}
