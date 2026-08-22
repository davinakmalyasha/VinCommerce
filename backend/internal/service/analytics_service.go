package service

import (
	"context"
	"time"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// AnalyticsService computes reports for admins and sellers.
type AnalyticsService struct {
	analytics *repository.AnalyticsRepository
	orders    *repository.OrderRepository
}

// NewAnalyticsService creates an AnalyticsService.
func NewAnalyticsService(analytics *repository.AnalyticsRepository) *AnalyticsService {
	return &AnalyticsService{analytics: analytics}
}

// SetOrders enables the seller order CSV export.
func (s *AnalyticsService) SetOrders(o *repository.OrderRepository) { s.orders = o }

// SellerOrders lists the seller's orders for export.
func (s *AnalyticsService) SellerOrders(ctx context.Context, sellerID string, limit int) ([]*domain.Order, error) {
	if s.orders == nil {
		return nil, domain.E(domain.KindConflict, "UNAVAILABLE", "order export unavailable")
	}
	orders, _, err := s.orders.ListBySeller(ctx, sellerID, "", 1, limit)
	return orders, err
}

// Report is the full analytics payload.
type Report struct {
	Summary          *repository.Summary         `json:"summary"`
	SalesSeries      []*repository.SalesPoint    `json:"sales_series"`
	TopProducts      []*repository.TopProduct    `json:"top_products"`
	Funnel           []*repository.FunnelStep    `json:"funnel"`
	CategorySales    []*repository.CategorySales `json:"category_sales"`
	PaymentSplit     []*repository.PaymentSplit  `json:"payment_split"`
	BuyerCohorts     []*repository.BuyerCohort   `json:"buyer_cohorts"`
	CommissionEarned float64                     `json:"commission_earned"`
	TopSellers       []*repository.TopSeller     `json:"top_sellers"`
	CouponStats      []*repository.CouponStat    `json:"coupon_stats"`
}

// PlatformReport returns platform-wide analytics (admin).
func (s *AnalyticsService) PlatformReport(ctx context.Context, from, to time.Time) (*Report, error) {
	return s.report(ctx, from, to, "")
}

// SellerReport returns analytics scoped to one store.
func (s *AnalyticsService) SellerReport(ctx context.Context, sellerID string, from, to time.Time) (*Report, error) {
	return s.report(ctx, from, to, sellerID)
}

func (s *AnalyticsService) report(ctx context.Context, from, to time.Time, sellerID string) (*Report, error) {
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -29)
	}

	summary, err := s.analytics.Overview(ctx, from, to, sellerID)
	if err != nil {
		return nil, err
	}
	series, err := s.analytics.SalesSeries(ctx, from, to, sellerID)
	if err != nil {
		return nil, err
	}
	top, err := s.analytics.TopProducts(ctx, from, to, sellerID, 10)
	if err != nil {
		return nil, err
	}
	funnel, err := s.analytics.Funnel(ctx, from, to, sellerID)
	if err != nil {
		return nil, err
	}
	cats, err := s.analytics.CategorySalesBy(ctx, from, to, sellerID)
	if err != nil {
		return nil, err
	}
	cohorts, err := s.analytics.BuyerCohorts(ctx, from, to, sellerID)
	if err != nil {
		return nil, err
	}
	r := &Report{Summary: summary, SalesSeries: series, TopProducts: top, Funnel: funnel, CategorySales: cats, BuyerCohorts: cohorts}
	if sellerID == "" {
		split, err := s.analytics.PaymentSplitBy(ctx, from, to)
		if err != nil {
			return nil, err
		}
		r.PaymentSplit = split
		commission, err := s.analytics.CommissionEarned(ctx, from, to)
		if err != nil {
			return nil, err
		}
		r.CommissionEarned = commission
		topSellers, err := s.analytics.TopSellers(ctx, from, to, 10)
		if err != nil {
			return nil, err
		}
		r.TopSellers = topSellers
		coupons, err := s.analytics.CouponStats(ctx, from, to, 10)
		if err != nil {
			return nil, err
		}
		r.CouponStats = coupons
	}
	return r, nil
}

// RecordView logs a product view event (optional user).
func (s *AnalyticsService) RecordView(ctx context.Context, productID string, userID *string) error {
	return s.analytics.RecordProductView(ctx, productID, userID)
}
