package service

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// ProductService implements catalog queries and review flows.
type ProductService struct {
	products *repository.ProductRepository
	reviews  *repository.ReviewRepository
	orders   *repository.OrderRepository
	notifs   *NotificationService
}

// NewProductService creates a ProductService.
func NewProductService(products *repository.ProductRepository, reviews *repository.ReviewRepository, orders *repository.OrderRepository) *ProductService {
	return &ProductService{products: products, reviews: reviews, orders: orders}
}

// SetNotificationService enables moderation notifications.
func (s *ProductService) SetNotificationService(n *NotificationService) { s.notifs = n }

// ReportProductInput for a buyer's report.
type ReportProductInput struct {
	ProductID   string
	UserID      string
	Reason      string
	Description string
}

var reportReasons = map[string]bool{
	"fake": true, "prohibited": true, "copyright": true, "misleading": true, "other": true,
}

// ReportProduct opens a moderation report against a product.
func (s *ProductService) ReportProduct(ctx context.Context, in ReportProductInput) error {
	if !reportReasons[in.Reason] {
		return domain.E(domain.KindInvalid, "BAD_REASON", "invalid report reason")
	}
	product, err := s.products.ByIDAnyStatus(ctx, in.ProductID)
	if err != nil {
		return err
	}
	if product.SellerID == in.UserID {
		return domain.E(domain.KindInvalid, "SELF_REPORT", "cannot report your own product")
	}
	return s.products.CreateReport(ctx, uuid.NewString(), in.ProductID, in.UserID, in.Reason, strings.TrimSpace(in.Description))
}

// AdminReports lists open product reports with product context.
func (s *ProductService) AdminReports(ctx context.Context, status string) ([]*repository.ProductReport, error) {
	return s.products.ListReports(ctx, status)
}

// ResolveReport closes a report; takedown deactivates the product and notifies its seller.
func (s *ProductService) ResolveReport(ctx context.Context, reportID, note string, takedown bool) error {
	if err := s.products.ResolveReport(ctx, reportID, note); err != nil {
		return err
	}
	if !takedown {
		return nil
	}
	report, err := s.products.ReportByID(ctx, reportID)
	if err != nil {
		return err
	}
	product, err := s.products.ByIDAnyStatus(ctx, report.ProductID)
	if err != nil {
		return err
	}
	if err := s.products.SetStatus(ctx, product.ID, domain.ProductInactive); err != nil {
		return err
	}
	if s.notifs != nil {
		_ = s.notifs.Notify(ctx, product.SellerID, "moderation", "Produk dinonaktifkan oleh admin",
			product.Name+" ditarik karena laporan: "+report.Reason, map[string]any{"product_id": product.ID, "product_slug": product.Slug})
	}
	return nil
}

// Search performs the faceted product search.
func (s *ProductService) Search(ctx context.Context, f *domain.ProductFilter) (*domain.SearchResult, error) {
	return s.products.Search(ctx, f)
}

// BySlug fetches an active product with detail.
func (s *ProductService) BySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return s.products.BySlug(ctx, slug)
}

// ByID fetches an active product by id.
func (s *ProductService) ByID(ctx context.Context, id string) (*domain.Product, error) {
	return s.products.ByID(ctx, id)
}

// Related returns similar products.
func (s *ProductService) Related(ctx context.Context, productID string, limit int) ([]*domain.Product, error) {
	return s.products.Related(ctx, productID, limit)
}

// ReviewsByProduct lists approved reviews.
func (s *ProductService) ReviewsByProduct(ctx context.Context, productID string, page, pageSize int, sort string) ([]*domain.ProductReview, int64, error) {
	return s.reviews.ListByProduct(ctx, productID, page, pageSize, sort)
}

// CustomerPhotos flattens approved review images into a PDP gallery strip.
func (s *ProductService) CustomerPhotos(ctx context.Context, productID string, limit int) ([]*repository.CustomerPhoto, error) {
	return s.reviews.CustomerPhotos(ctx, productID, limit)
}

// RatingDistribution returns the star histogram for a product.
func (s *ProductService) RatingDistribution(ctx context.Context, productID string) ([]*domain.RatingCount, error) {
	return s.reviews.RatingDistribution(ctx, productID)
}

// CreateReviewInput for posting a review.
type CreateReviewInput struct {
	ProductID   string
	UserID      string
	OrderItemID *string
	Rating      int
	Title       string
	Content     string
	Images      []string
}

// CreateReview records a new review, pending moderation.
func (s *ProductService) CreateReview(ctx context.Context, in CreateReviewInput) (*domain.ProductReview, error) {
	if in.Rating < 1 || in.Rating > 5 {
		return nil, domain.E(domain.KindInvalid, "INVALID_RATING", "rating must be between 1 and 5")
	}
	if strings.TrimSpace(in.Content) == "" && strings.TrimSpace(in.Title) == "" {
		return nil, domain.E(domain.KindInvalid, "EMPTY_REVIEW", "review text is required")
	}
	// Integrity: a claimed order item must belong to the caller AND to this
	// product, otherwise anyone could forge "verified purchase" badges.
	if in.OrderItemID != nil && *in.OrderItemID != "" {
		item, err := s.orders.ItemByID(ctx, *in.OrderItemID)
		if err != nil {
			return nil, domain.E(domain.KindInvalid, "BAD_ORDER_ITEM", "order item not found")
		}
		order, err := s.orders.ByID(ctx, item.OrderID)
		if err != nil || order.BuyerID != in.UserID {
			return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order item does not belong to user")
		}
		if item.ProductID != in.ProductID {
			return nil, domain.E(domain.KindInvalid, "PRODUCT_MISMATCH", "order item is for a different product")
		}
	}
	rev := &domain.ProductReview{
		ID:          uuid.NewString(),
		ProductID:   in.ProductID,
		UserID:      in.UserID,
		OrderItemID: in.OrderItemID,
		Rating:      in.Rating,
		Title:       strings.TrimSpace(in.Title),
		Content:     strings.TrimSpace(in.Content),
		Images:      in.Images,
		Status:      domain.ReviewPending,
	}
	if err := s.reviews.Create(ctx, rev); err != nil {
		return nil, err
	}
	return rev, nil
}

// ToggleReviewHelpful flips a helpful vote.
func (s *ProductService) ToggleReviewHelpful(ctx context.Context, reviewID, userID string) (bool, int, error) {
	return s.reviews.ToggleHelpful(ctx, reviewID, userID)
}

// PendingReviews lists reviews awaiting moderation (admin).
func (s *ProductService) PendingReviews(ctx context.Context, limit int) ([]*domain.ProductReview, error) {
	return s.reviews.Pending(ctx, limit)
}

// ModerateReview approves or rejects a review (admin).
func (s *ProductService) ModerateReview(ctx context.Context, reviewID, status, note string) error {
	switch status {
	case domain.ReviewApproved, domain.ReviewRejected:
	default:
		return domain.E(domain.KindInvalid, "BAD_STATUS", "status must be approved or rejected")
	}
	return s.reviews.UpdateStatus(ctx, reviewID, status, note)
}

// ReplyToReview lets the product's seller reply to a review (once).
func (s *ProductService) ReplyToReview(ctx context.Context, reviewID, userID, content string) error {
	if strings.TrimSpace(content) == "" {
		return domain.E(domain.KindInvalid, "CONTENT_REQUIRED", "reply content is required")
	}
	// resolve review product + seller
	var sellerID string
	err := s.reviews.ProductSeller(ctx, reviewID, &sellerID)
	if err != nil {
		return err
	}
	if sellerID != userID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "only the product seller may reply")
	}
	return s.reviews.Reply(ctx, reviewID, userID, strings.TrimSpace(content))
}

// SellerReviews lists approved reviews across the seller's products.
func (s *ProductService) SellerReviews(ctx context.Context, sellerID string, limit int) ([]*repository.SellerReview, error) {
	return s.reviews.ListBySeller(ctx, sellerID, limit)
}

// ReviewOrderItemInput for reviewing a purchased item.
type ReviewOrderItemInput struct {
	OrderID string
	ItemID  string
	UserID  string
	Rating  int
	Title   string
	Content string
	Images  []string
}

// ReviewOrderItem lets a buyer review an item from a completed order.
func (s *ProductService) ReviewOrderItem(ctx context.Context, in ReviewOrderItemInput) (*domain.ProductReview, error) {
	order, err := s.orders.ByID(ctx, in.OrderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != in.UserID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if order.Status != domain.OrderDelivered && order.Status != domain.OrderCompleted {
		return nil, domain.E(domain.KindConflict, "ORDER_NOT_FINISHED", "reviews are available after delivery")
	}
	var item *domain.OrderItem
	for _, it := range order.Items {
		if it.ID == in.ItemID {
			item = it
			break
		}
	}
	if item == nil {
		return nil, domain.E(domain.KindNotFound, "ITEM_NOT_FOUND", "order item not found")
	}
	return s.CreateReview(ctx, CreateReviewInput{
		ProductID: item.ProductID, UserID: in.UserID, OrderItemID: &item.ID,
		Rating: in.Rating, Title: in.Title, Content: in.Content, Images: in.Images,
	})
}
