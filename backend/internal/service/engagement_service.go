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
	stores   *repository.StoreRepository
	cache    *cache.Store
}

// NewEngagementService creates an EngagementService.
func NewEngagementService(wishlist *repository.WishlistRepository, products *repository.ProductRepository) *EngagementService {
	return &EngagementService{wishlist: wishlist, products: products}
}

// SetStores enables followed-store buckets in the feed.
func (s *EngagementService) SetStores(st *repository.StoreRepository) { s.stores = st }

// FeedItem pairs a product with the feed bucket it came from.
type FeedItem struct {
	Bucket    string          `json:"bucket"` // flash_deal | followed | bestseller
	Product   *domain.Product `json:"product"`
	SalePrice *float64        `json:"sale_price,omitempty"`
}

// Feed mixes flash deals, followed-store drops and bestsellers into one
// interleaved discovery stream. `page` slices deeper into each bucket.
func (s *EngagementService) Feed(ctx context.Context, userID string, page, limit int) ([]*FeedItem, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 30 {
		limit = 12
	}
	per := limit // take `limit` candidates from each bucket, then interleave

	buckets := [][]*FeedItem{}

	// flash deals
	if sale, err := s.wishlist.ActiveFlashSale(ctx); err == nil {
		if items, err := s.wishlist.FlashSaleItems(ctx, sale.ID); err == nil && len(items) > 0 {
			b := make([]*FeedItem, 0, len(items))
			for _, it := range items {
				sp := it.SalePrice
				b = append(b, &FeedItem{
					Bucket: "flash_deal",
					Product: &domain.Product{
						ID: it.ProductID, Name: it.ProductName, Slug: it.ProductSlug,
					},
					SalePrice: &sp,
				})
			}
			buckets = append(buckets, b)
		}
	}

	// new from followed stores (logged-in only)
	if userID != "" && s.stores != nil {
		if items, err := s.stores.RecentProductsFromFollowed(ctx, userID, per*page); err == nil {
			b := make([]*FeedItem, 0, len(items))
			for _, p := range items {
				b = append(b, &FeedItem{Bucket: "followed", Product: p})
			}
			buckets = append(buckets, b)
		}
	}

	// bestsellers
	if items, err := s.products.Bestsellers(ctx, per*page); err == nil {
		b := make([]*FeedItem, 0, len(items))
		for _, p := range items {
			b = append(b, &FeedItem{Bucket: "bestseller", Product: p})
		}
		buckets = append(buckets, b)
	}

	// round-robin interleave with dedupe into a stable stream, then slice.
	total := per * page
	full := make([]*FeedItem, 0, total)
	seen := map[string]bool{}
	for i := 0; len(full) < total; i++ {
		emitted := false
		for _, b := range buckets {
			if i >= len(b) {
				continue
			}
			item := b[i]
			if item.Product != nil && seen[item.Product.ID] {
				continue
			}
			if item.Product != nil {
				seen[item.Product.ID] = true
			}
			full = append(full, item)
			emitted = true
			if len(full) >= total {
				break
			}
		}
		if !emitted {
			break // every bucket exhausted
		}
	}

	start := (page - 1) * limit
	if start >= len(full) {
		return []*FeedItem{}, nil
	}
	end := min(start+limit, len(full))
	return full[start:end], nil
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
