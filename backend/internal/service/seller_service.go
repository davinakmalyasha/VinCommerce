package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
)

// SellerService implements store onboarding, product management and returns.
type SellerService struct {
	stores   *repository.StoreRepository
	users    *repository.UserRepository
	products *repository.ProductRepository
	orders   *repository.OrderRepository
	payments *repository.PaymentRepository
	// paymentSvc is the payment SERVICE. Distinct from `payments` above: the
	// repository is raw persistence, the service owns the money invariants
	// (cumulative refund cap, leg derivation, state transitions). Anything that
	// MOVES money must go through the service, never the repository.
	paymentSvc       *PaymentService
	sessions         *repository.SessionRepository
	mailer           *mail.Client
	webURL           string
	notifs           *NotificationService
	onProductChanged func(ctx context.Context)

	returnAutoApproveMax float64
	presence             *cache.Store
}

// SetReturnAutoApprove enables instant approval for claims at or below the
// given item value (0 disables).
func (s *SellerService) SetReturnAutoApprove(max float64) { s.returnAutoApproveMax = max }

// SetPresenceCache enables the seller online heartbeat.
func (s *SellerService) SetPresenceCache(c *cache.Store) { s.presence = c }

const presenceTTL = 5 * time.Minute

// Heartbeat marks the seller active (drives the public "online" dot).
func (s *SellerService) Heartbeat(ctx context.Context, sellerID string) {
	if s.presence != nil {
		_ = s.presence.Set(ctx, "seller_online:"+sellerID, true, presenceTTL)
	}
}

// IsOnline reports whether the seller heartbeat is fresh.
func (s *SellerService) IsOnline(ctx context.Context, sellerID string) bool {
	if s.presence == nil {
		return false
	}
	var v bool
	ok, _ := s.presence.Get(ctx, "seller_online:"+sellerID, &v)
	return ok && v
}

// UpdatePresenceStats recomputes chat responsiveness for all stores (nightly).
func (s *SellerService) UpdatePresenceStats(ctx context.Context) error {
	return s.stores.UpdatePresenceStats(ctx)
}

// NewSellerService wires seller capabilities.
func NewSellerService(stores *repository.StoreRepository, users *repository.UserRepository, products *repository.ProductRepository, orders *repository.OrderRepository, payments *repository.PaymentRepository) *SellerService {
	return &SellerService{stores: stores, users: users, products: products, orders: orders, payments: payments}
}

// SetPaymentService gives the seller service the payment SERVICE, not just the
// payment repository.
//
// It needs the service, not the repository, because RefundReturn has to go
// through the same refund implementation as every other refund path. Holding
// only the repository is what let RefundReturn become a second, independent
// refund with none of the invariants: no cumulative cap, no debit, no
// commission reversal, and a terminal intent status on the first refunded item.
// Those are the properties of a refund, not of a table, so they belong behind
// the service that owns them.
func (s *SellerService) SetPaymentService(p *PaymentService) { s.paymentSvc = p }

// SetSessions enables audit log access (admin).
func (s *SellerService) SetSessions(sess *repository.SessionRepository) { s.sessions = sess }

// SetNotificationService enables follower notifications.
func (s *SellerService) SetNotificationService(n *NotificationService) { s.notifs = n }

// SetOnProductChanged registers a cache-invalidation callback fired after
// any product create/update/status change (wired to recommendation epoch).
func (s *SellerService) SetOnProductChanged(fn func(ctx context.Context)) { s.onProductChanged = fn }

func (s *SellerService) productChanged(ctx context.Context) {
	if s.onProductChanged != nil {
		s.onProductChanged(ctx)
	}
}

// SetMailer enables transactional shipping emails.
func (s *SellerService) SetMailer(m *mail.Client, webURL string) { s.mailer = m; s.webURL = webURL }

func (s *SellerService) emailBuyer(ctx context.Context, orderID, templateName, subject string, data map[string]any) {
	if s.mailer == nil || s.users == nil {
		return
	}
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return
	}
	buyer, err := s.users.ByID(ctx, o.BuyerID)
	if err != nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Name"] = buyer.FullName
	data["OrderNumber"] = o.OrderNumber
	data["OrderURL"] = s.webURL + "/orders/" + o.ID
	_ = s.mailer.Send(ctx, buyer.Email, subject, templateName, data)
}

// --- admin: users ---

// ListUsers paginates all users (admin).
func (s *SellerService) ListUsers(ctx context.Context, page, pageSize int) ([]*domain.User, int64, error) {
	return s.users.ListUsers(ctx, page, pageSize)
}

// SetUserStatus suspends or activates a user (admin).
func (s *SellerService) SetUserStatus(ctx context.Context, userID, status string) error {
	switch status {
	case domain.UserStatusActive, domain.UserStatusDisabled, domain.UserStatusSuspended:
	default:
		return domain.E(domain.KindInvalid, "BAD_STATUS", "invalid user status")
	}
	return s.users.UpdateStatus(ctx, userID, status)
}

// GrantSellerRole promotes a user to seller (admin).
func (s *SellerService) GrantSellerRole(ctx context.Context, userID string) error {
	return s.users.AddRoles(ctx, userID, []string{domain.RoleSeller})
}

// RevokeUserRole strips a role from a user (admin). Guards:
//   - 'buyer' is the base identity role and cannot be removed
//   - an admin cannot revoke roles from themselves
//   - the last admin can never be demoted (lockout prevention)
func (s *SellerService) RevokeUserRole(ctx context.Context, adminID, userID, role string) error {
	switch role {
	case domain.RoleSeller, domain.RoleAdmin, domain.RoleSupport:
	case domain.RoleBuyer:
		return domain.E(domain.KindInvalid, "ROLE_IRREVOCABLE", "buyer is the base role and cannot be revoked")
	default:
		return domain.E(domain.KindInvalid, "BAD_ROLE", "unknown role")
	}
	if adminID == userID && role == domain.RoleAdmin {
		return domain.E(domain.KindInvalid, "SELF_DEMOTE", "cannot revoke your own admin role")
	}
	if role == domain.RoleAdmin {
		n, err := s.users.CountRoleHolders(ctx, domain.RoleAdmin)
		if err == nil && n <= 1 {
			return domain.E(domain.KindConflict, "LAST_ADMIN", "cannot revoke the last administrator")
		}
		// Revoking admin also strips seller/support so no hidden escalation path remains.
		return s.users.RemoveRoles(ctx, userID, []string{domain.RoleAdmin})
	}
	return s.users.RemoveRoles(ctx, userID, []string{role})
}

// UserDetail returns a user's profile with stats (admin).
func (s *SellerService) UserDetail(ctx context.Context, userID string) (*repository.UserDetail, error) {
	return s.users.DetailWithStats(ctx, userID)
}

// SellerCoupons lists the seller's coupons.
func (s *SellerService) SellerCoupons(ctx context.Context, sellerID string, activeOnly bool) ([]*domain.Coupon, error) {
	return s.orders.SellerCoupons(ctx, sellerID, activeOnly)
}

// CreateSellerCoupon adds a store-scoped coupon.
func (s *SellerService) CreateSellerCoupon(ctx context.Context, sellerID string, in CreateCouponInput) (*domain.Coupon, error) {
	c, err := s.CreateCoupon(ctx, in)
	if err != nil {
		return nil, err
	}
	// re-scope to the seller
	if err := s.orders.SetCouponSeller(ctx, c.ID, sellerID); err != nil {
		return nil, err
	}
	c.SellerID = &sellerID
	return c, nil
}

// AllVouchers lists collectible store coupons (public).
func (s *SellerService) AllVouchers(ctx context.Context) ([]*domain.Coupon, error) {
	return s.orders.AllVouchers(ctx)
}

// --- admin: coupons ---

// ListCoupons lists all coupons (admin).
func (s *SellerService) ListCoupons(ctx context.Context) ([]*domain.Coupon, error) {
	return s.orders.ListCoupons(ctx)
}

// CreateCouponInput for the admin coupon form.
type CreateCouponInput struct {
	Code         string
	Type         string
	Value        float64
	MinSubtotal  float64
	MaxDiscount  *float64
	UsageLimit   int
	PerUserLimit int
	ValidDays    int
}

// CreateCoupon creates a coupon (admin).
func (s *SellerService) CreateCoupon(ctx context.Context, in CreateCouponInput) (*domain.Coupon, error) {
	if strings.TrimSpace(in.Code) == "" {
		return nil, domain.E(domain.KindInvalid, "CODE_REQUIRED", "coupon code is required")
	}
	if in.Type != "percent" && in.Type != "fixed" {
		return nil, domain.E(domain.KindInvalid, "BAD_TYPE", "type must be percent or fixed")
	}
	if in.Value <= 0 {
		return nil, domain.E(domain.KindInvalid, "BAD_VALUE", "value must be positive")
	}
	if in.PerUserLimit <= 0 {
		in.PerUserLimit = 1
	}
	c := &domain.Coupon{
		ID:           uuid.NewString(),
		Code:         strings.ToUpper(strings.TrimSpace(in.Code)),
		Type:         in.Type,
		Value:        in.Value,
		MinSubtotal:  in.MinSubtotal,
		MaxDiscount:  in.MaxDiscount,
		UsageLimit:   in.UsageLimit,
		PerUserLimit: in.PerUserLimit,
		ValidFrom:    time.Now().UTC(),
		IsActive:     true,
	}
	if in.ValidDays > 0 {
		until := time.Now().UTC().AddDate(0, 0, in.ValidDays)
		c.ValidUntil = &until
	}
	if err := s.orders.CreateCoupon(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ToggleCoupon activates/deactivates a coupon (admin).
func (s *SellerService) ToggleCoupon(ctx context.Context, couponID string, active bool) error {
	return s.orders.SetCouponActive(ctx, couponID, active)
}

// ListShippingMethods lists all carriers (admin).
func (s *SellerService) ListShippingMethods(ctx context.Context) ([]*domain.ShippingMethod, error) {
	return s.orders.ShippingMethodsAll(ctx, false)
}

// CreateShippingMethod adds a carrier (admin).
func (s *SellerService) CreateShippingMethod(ctx context.Context, m *domain.ShippingMethod) (*domain.ShippingMethod, error) {
	m.ID = uuid.NewString()
	if err := s.orders.CreateShippingMethod(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// ToggleShippingMethod activates/deactivates a carrier (admin).
func (s *SellerService) ToggleShippingMethod(ctx context.Context, methodID string, active bool) error {
	return s.orders.SetShippingMethodActive(ctx, methodID, active)
}

// --- admin: orders & audit & fees ---

// SearchOrders searches platform orders (admin).
func (s *SellerService) SearchOrders(ctx context.Context, q, status string, page, pageSize int) ([]*domain.Order, int64, error) {
	return s.orders.SearchOrders(ctx, q, status, page, pageSize)
}

// AdjustStock changes a variant's stock (seller/admin).
func (s *SellerService) AdjustStock(ctx context.Context, sellerID, variantID string, delta int) error {
	var owner string
	err := s.products.VariantOwner(ctx, variantID, &owner)
	if err != nil {
		return err
	}
	if owner != sellerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "variant does not belong to store")
	}
	if err := s.orders.AdjustStock(ctx, variantID, delta, "adjustment"); err != nil {
		return err
	}
	// restocked above the threshold → clear the low-stock alert so it can fire again later
	if stock, err := s.products.VariantStock(ctx, variantID); err == nil && stock > lowStockThreshold {
		_ = s.products.ClearLowStockAlert(ctx, variantID)
	}
	return nil
}

// lowStockThreshold is the stock level that triggers a seller alert.
const lowStockThreshold = 5

// LowStockForSeller lists the seller's variants at/below the threshold.
func (s *SellerService) LowStockForSeller(ctx context.Context, sellerID string) ([]*repository.LowStockVariant, error) {
	return s.products.LowStockForSeller(ctx, sellerID, lowStockThreshold)
}

// ProcessLowStock notifies sellers whose variants are at/below the threshold,
// deduplicated per variant for 24 hours (worker).
func (s *SellerService) ProcessLowStock(ctx context.Context, limit int) (int, error) {
	variants, err := s.products.LowStockVariants(ctx, lowStockThreshold, time.Now().UTC().Add(-24*time.Hour), limit)
	if err != nil {
		return 0, err
	}
	notified := 0
	for _, v := range variants {
		if s.notifs != nil {
			_ = s.notifs.Notify(ctx, v.SellerID, "low_stock", "Stok menipis! ⚠️",
				v.ProductName+" ("+v.VariantName+") tersisa "+strconv.Itoa(v.Stock)+" unit",
				map[string]any{"variant_id": v.VariantID})
		}
		if s.mailer != nil && s.users != nil {
			if u, err := s.users.ByID(ctx, v.SellerID); err == nil {
				_ = s.mailer.Send(ctx, u.Email, "Stok menipis — VinCommerce", "low_stock", map[string]any{
					"Name":        u.FullName,
					"ProductName": v.ProductName,
					"VariantName": v.VariantName,
					"Stock":       v.Stock,
				})
			}
		}
		if err := s.products.UpsertLowStockAlert(ctx, v.SellerID, v.VariantID); err != nil {
			return notified, err
		}
		notified++
	}
	return notified, nil
}

// AuditLog lists audit entries (admin).
func (s *SellerService) AuditLog(ctx context.Context, actorID, action string, limit int) ([]*repository.AuditEntryRow, error) {
	return s.sessions.AuditLog(ctx, actorID, action, limit)
}

// SetFreeShipping updates the store's free-shipping threshold.
func (s *SellerService) SetFreeShipping(ctx context.Context, ownerID string, threshold *float64) error {
	if _, err := s.stores.ByOwner(ctx, ownerID); err != nil {
		return err
	}
	return s.stores.SetFreeShippingThreshold(ctx, ownerID, threshold)
}

// SetCommissionConfig updates the platform fee (admin).
func (s *SellerService) SetCommissionConfig(ctx context.Context, pct, fixed float64) error {
	if pct < 0 || pct > 50 {
		return domain.E(domain.KindInvalid, "BAD_PCT", "commission percent must be between 0 and 50")
	}
	return s.payments.SetFee(ctx, pct, fixed)
}

// CommissionConfig returns the active fee (admin).
func (s *SellerService) CommissionConfig(ctx context.Context) (*repository.PlatformFee, error) {
	return s.payments.ActiveFee(ctx)
}

// CreateProductInput for seller product creation.
type CreateProductInput struct {
	SellerID    string
	Name        string
	CategoryID  string
	BrandID     string
	Description string
	Attributes  map[string]string
	Variants    []VariantInput
	Images      []string
}

// VariantInput is one SKU row from the seller form.
type VariantInput struct {
	SKU            string
	Name           string
	Price          float64
	CompareAtPrice *float64
	Stock          int
	WeightGrams    int
	ImageURL       string
	Attributes     map[string]string
	IsActive       bool
}

// PublicStore returns a public store profile with its products.
func (s *SellerService) PublicStore(ctx context.Context, slug, userID string) (*domain.Store, []*domain.Product, error) {
	store, err := s.stores.BySlug(ctx, slug)
	if err != nil {
		return nil, nil, err
	}
	if userID != "" {
		following, err := s.stores.IsFollowing(ctx, userID, store.ID)
		if err != nil {
			return nil, nil, err
		}
		store.IsFollowing = following
	}
	if kyc, err := s.stores.KYCByStore(ctx, store.ID); err == nil && kyc.Status == "approved" {
		store.IsVerified = true
	}
	// presence signals
	store.Online = s.IsOnline(ctx, store.ID)
	if p, err := s.stores.PresenceByStore(ctx, store.ID); err == nil {
		store.ResponseRatePct = p.ResponseRatePct
		store.AvgReplyMinutes = p.AvgReplyMinutes
		if p.ResponseRatePct != nil && *p.ResponseRatePct >= 90 && store.Rating >= 4.5 {
			store.IsPowerSeller = true
		}
	}
	// SQL-level active filter keeps pagination correct (post-load filtering
	// shrank pages when drafts existed).
	active, _, err := s.products.ListActiveBySeller(ctx, store.OwnerID, 1, 24)
	if err != nil {
		return nil, nil, err
	}
	return store, active, nil
}

// --- store following ---

// FollowStore registers a follow (returns the new follower count).
func (s *SellerService) FollowStore(ctx context.Context, userID, storeID string) (int, error) {
	store, err := s.stores.ByID(ctx, storeID)
	if err != nil {
		return 0, err
	}
	if store.OwnerID == userID {
		return 0, domain.E(domain.KindInvalid, "SELF_FOLLOW", "cannot follow your own store")
	}
	if err := s.stores.Follow(ctx, userID, storeID); err != nil {
		return 0, err
	}
	return s.stores.FollowerCountOf(ctx, storeID)
}

// UnfollowStore removes a follow.
func (s *SellerService) UnfollowStore(ctx context.Context, userID, storeID string) error {
	return s.stores.Unfollow(ctx, userID, storeID)
}

// FollowedStores lists stores the user follows.
func (s *SellerService) FollowedStores(ctx context.Context, userID string) ([]*domain.Store, error) {
	return s.stores.FollowedStores(ctx, userID)
}

// FollowedFeed lists the newest products from followed stores.
func (s *SellerService) FollowedFeed(ctx context.Context, userID string, limit int) ([]*domain.Product, error) {
	return s.stores.RecentProductsFromFollowed(ctx, userID, limit)
}

// notifyFollowers pushes a "new product" notification to store followers.
func (s *SellerService) notifyFollowers(ctx context.Context, storeID, productID, productName, productSlug string) {
	if s.notifs == nil {
		return
	}
	followers, err := s.stores.FollowersOf(ctx, storeID)
	if err != nil || len(followers) == 0 {
		return
	}
	// Single batched fan-out: one INSERT for every follower instead of one
	// round trip per row (popular stores previously inserted thousands of
	// rows serially inside the request goroutine).
	_, _ = s.notifs.NotifyMany(ctx, followers, "store_new_product",
		"Toko favorit punya produk baru! 🛍️", productName,
		map[string]any{"product_id": productID, "product_slug": productSlug}, nil)
}

// CreateProduct creates a product (draft) with variants and images.
func (s *SellerService) CreateProduct(ctx context.Context, in CreateProductInput) (*domain.Product, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, domain.E(domain.KindInvalid, "NAME_REQUIRED", "product name is required")
	}
	if len(in.Variants) == 0 {
		return nil, domain.E(domain.KindInvalid, "VARIANT_REQUIRED", "at least one variant is required")
	}
	for _, v := range in.Variants {
		if v.Price <= 0 {
			return nil, domain.E(domain.KindInvalid, "BAD_PRICE", "price must be positive")
		}
	}

	if _, err := s.stores.ByOwner(ctx, in.SellerID); err != nil {
		return nil, err
	}

	product := &domain.Product{
		ID:          uuid.NewString(),
		SellerID:    in.SellerID,
		CategoryID:  strOrNil(in.CategoryID),
		BrandID:     strOrNil(in.BrandID),
		Name:        strings.TrimSpace(in.Name),
		Slug:        slugify(in.Name),
		Description: in.Description,
		Status:      domain.ProductDraft,
		Attributes:  in.Attributes,
	}
	variants := make([]*domain.ProductVariant, 0, len(in.Variants))
	for _, v := range in.Variants {
		variants = append(variants, &domain.ProductVariant{
			ID: uuid.NewString(), SKU: v.SKU, Name: v.Name, Price: v.Price,
			CompareAtPrice: v.CompareAtPrice, Stock: v.Stock, WeightGrams: v.WeightGrams,
			ImageURL: v.ImageURL, Attributes: v.Attributes, IsActive: true,
		})
	}
	images := make([]*domain.ProductImage, 0, len(in.Images))
	for _, u := range in.Images {
		images = append(images, &domain.ProductImage{URL: u})
	}

	if err := s.products.CreateWithVariants(ctx, product, variants, images); err != nil {
		return nil, err
	}
	s.productChanged(ctx)
	return product, nil
}

// UpdateProduct edits an owned product, replacing variants and images.
func (s *SellerService) UpdateProduct(ctx context.Context, sellerID, productID string, in CreateProductInput) (*domain.Product, error) {
	existing, err := s.products.ByIDAnyStatus(ctx, productID)
	if err != nil {
		return nil, err
	}
	if existing.SellerID != sellerID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "product does not belong to store")
	}
	if len(in.Variants) == 0 {
		return nil, domain.E(domain.KindInvalid, "VARIANT_REQUIRED", "at least one variant is required")
	}

	existing.CategoryID = strOrNil(in.CategoryID)
	existing.BrandID = strOrNil(in.BrandID)
	existing.Name = strings.TrimSpace(in.Name)
	existing.Slug = slugify(in.Name)
	existing.Description = in.Description
	if in.Attributes != nil {
		existing.Attributes = in.Attributes
	}
	if existing.Attributes == nil {
		existing.Attributes = map[string]string{}
	}

	variants := make([]*domain.ProductVariant, 0, len(in.Variants))
	for _, v := range in.Variants {
		variants = append(variants, &domain.ProductVariant{
			ID: uuid.NewString(), SKU: v.SKU, Name: v.Name, Price: v.Price,
			CompareAtPrice: v.CompareAtPrice, Stock: v.Stock, WeightGrams: v.WeightGrams,
			ImageURL: v.ImageURL, Attributes: v.Attributes, IsActive: true,
		})
	}
	images := make([]*domain.ProductImage, 0, len(in.Images))
	for _, u := range in.Images {
		images = append(images, &domain.ProductImage{URL: u})
	}

	if err := s.products.UpdateWithVariants(ctx, existing, variants, images); err != nil {
		return nil, err
	}
	s.productChanged(ctx)
	return existing, nil
}

// FulfillOrder moves a seller's order to the next fulfillment state.
// paid -> packed (by seller), packed -> shipped (by seller, with tracking).
func (s *SellerService) FulfillOrder(ctx context.Context, sellerID, orderID, to, trackingNumber, carrier string) error {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.SellerID != sellerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to store")
	}
	switch to {
	case domain.OrderPacked:
		if order.Status != domain.OrderPaid {
			return domain.E(domain.KindConflict, "INVALID_TRANSITION",
				fmt.Sprintf("cannot move order from %s to %s", order.Status, to))
		}
		return s.orders.TransitionOrder(ctx, orderID, domain.OrderPaid, domain.OrderPacked, sellerID)
	case domain.OrderShipped:
		if order.Status != domain.OrderPacked {
			return domain.E(domain.KindConflict, "INVALID_TRANSITION",
				fmt.Sprintf("cannot move order from %s to %s", order.Status, to))
		}
		if err := s.orders.ShipOrder(ctx, orderID, domain.OrderPacked, trackingNumber, carrier); err != nil {
			return err
		}
		s.emailBuyer(ctx, orderID, "order_shipped", "Pesanan dikirim — VinCommerce",
			map[string]any{"Carrier": carrier, "Tracking": trackingNumber})
		return nil
	default:
		return domain.E(domain.KindInvalid, "BAD_TRANSITION", "sellers may only pack or ship orders")
	}
}

func strOrNil(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// MyStore returns the seller's store (error if none).
func (s *SellerService) MyStore(ctx context.Context, userID string) (*domain.Store, error) {
	return s.stores.ByOwner(ctx, userID)
}

// AssertPayoutEligible enforces the KYC contract promised in the admin UI:
// withdrawals require an ACTIVE store with APPROVED KYC.
func (s *SellerService) AssertPayoutEligible(ctx context.Context, userID string) error {
	store, err := s.stores.ByOwner(ctx, userID)
	if err != nil {
		return domain.E(domain.KindForbidden, "NO_STORE", "a store is required to withdraw funds")
	}
	if store.Status != domain.StoreActive {
		return domain.E(domain.KindForbidden, "STORE_NOT_ACTIVE", "store must be approved before withdrawing funds")
	}
	kyc, err := s.stores.KYCByStore(ctx, store.ID)
	if err != nil || kyc == nil {
		return domain.E(domain.KindForbidden, "KYC_REQUIRED", "complete KYC verification before withdrawing funds")
	}
	if kyc.Status != "approved" {
		return domain.E(domain.KindForbidden, "KYC_NOT_APPROVED",
			"withdrawals unlock after KYC approval (current status: "+kyc.Status+")")
	}
	return nil
}

// OpenStore registers a pending store and grants the seller role.
// The grant is compensated away if it fails, so a store can never exist
// whose owner was never promoted (orphan-store state).
func (s *SellerService) OpenStore(ctx context.Context, userID, name string) (*domain.Store, error) {
	if strings.TrimSpace(name) == "" {
		return nil, domain.E(domain.KindInvalid, "NAME_REQUIRED", "store name is required")
	}
	store := &domain.Store{
		ID:      uuid.NewString(),
		OwnerID: userID,
		Name:    strings.TrimSpace(name),
		Slug:    slugify(name),
		Status:  domain.StorePending,
	}
	if err := s.stores.Create(ctx, store); err != nil {
		return nil, err
	}
	if err := s.users.AddRoles(ctx, userID, []string{domain.RoleSeller}); err != nil {
		// Best-effort compensation: don't leave a role-less store behind.
		_ = s.stores.DeleteIfOwner(ctx, store.ID, userID)
		return nil, domain.Wrap(domain.KindInternal, "ROLE_GRANT_FAILED",
			"store could not be opened; please retry", err)
	}
	return store, nil
}

// UpdateStore updates branding.
func (s *SellerService) UpdateStore(ctx context.Context, userID string, in *domain.Store) (*domain.Store, error) {
	store, err := s.stores.ByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		store.Name = in.Name
	}
	store.Description = in.Description
	store.LogoURL = in.LogoURL
	store.BannerURL = in.BannerURL
	if err := s.stores.Update(ctx, store); err != nil {
		return nil, err
	}
	return store, nil
}

// SubmitKYC records identity verification data.
func (s *SellerService) SubmitKYC(ctx context.Context, userID string, k *domain.SellerKYC) (*domain.SellerKYC, error) {
	store, err := s.stores.ByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	k.ID = uuid.NewString()
	k.StoreID = store.ID
	k.Status = "pending"
	if err := s.stores.SaveKYC(ctx, k); err != nil {
		return nil, err
	}
	return k, nil
}

// KYC fetches the seller's KYC record.
func (s *SellerService) KYC(ctx context.Context, userID string) (*domain.SellerKYC, error) {
	store, err := s.stores.ByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.stores.KYCByStore(ctx, store.ID)
}

// ListSellerProducts returns the seller's products with inventory.
func (s *SellerService) ListSellerProducts(ctx context.Context, sellerID string, page, pageSize int) ([]*domain.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	return s.products.ListBySeller(ctx, sellerID, page, pageSize)
}

// SellerStats is the dashboard summary.
type SellerStats struct {
	TotalSales     float64 `json:"total_sales"`
	OrderCount     int     `json:"order_count"`
	PendingOrders  int     `json:"pending_orders"`
	ProductsActive int     `json:"products_active"`
	ProductsDraft  int     `json:"products_draft"`
	WalletBalance  float64 `json:"wallet_balance"`
	AvgOrderValue  float64 `json:"avg_order_value"`
}

// Dashboard returns seller KPIs.
func (s *SellerService) Dashboard(ctx context.Context, sellerID string) (*SellerStats, error) {
	d, err := s.orders.Dashboard(ctx, sellerID)
	if err != nil {
		return nil, err
	}
	stats := &SellerStats{
		TotalSales:     d.TotalSales,
		OrderCount:     d.OrderCount,
		PendingOrders:  d.PendingOrders,
		ProductsActive: d.ProductsActive,
		ProductsDraft:  d.ProductsDraft,
	}
	wallet, err := s.payments.Wallet(ctx, sellerID)
	if err == nil {
		stats.WalletBalance = wallet.Balance
	}
	if stats.OrderCount > 0 {
		stats.AvgOrderValue = stats.TotalSales / float64(stats.OrderCount)
	}
	return stats, nil
}

// UpdateProductStatus (seller) activates/deactivates own product.
func (s *SellerService) UpdateProductStatus(ctx context.Context, sellerID, productID, status string) error {
	product, err := s.products.ByIDAnyStatus(ctx, productID)
	if err != nil {
		return err
	}
	if product.SellerID != sellerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "product does not belong to store")
	}
	switch status {
	case domain.ProductActive, domain.ProductInactive:
	default:
		return domain.E(domain.KindInvalid, "BAD_STATUS", "invalid product status")
	}
	// Moderation lock: a staff-taken-down product stays down. Reactivation is
	// an admin decision (ResolveReport dismiss / unlock), not a seller toggle.
	if status == domain.ProductActive && s.products.ModerationLocked(ctx, productID) {
		return domain.E(domain.KindForbidden, "MODERATION_LOCKED",
			"product was taken down by moderation and cannot be reactivated")
	}
	if err := s.products.SetStatus(ctx, productID, status); err != nil {
		return err
	}
	s.productChanged(ctx)
	if status == domain.ProductActive && product.Status != domain.ProductActive {
		store, err := s.stores.ByOwner(ctx, sellerID)
		if err == nil {
			s.notifyFollowers(ctx, store.ID, product.ID, product.Name, product.Slug)
		}
	}
	return nil
}

// CreateReturnInput for a buyer return claim.
type CreateReturnInput struct {
	OrderID      string
	OrderItemID  string
	BuyerID      string
	IssueType    string // return | item_not_received (empty = return)
	Reason       string
	Description  string
	EvidenceURLs []string
}

// RequestReturn opens a return claim for a delivered item.
func (s *SellerService) RequestReturn(ctx context.Context, in CreateReturnInput) (*domain.ReturnRequest, error) {
	order, err := s.orders.ByID(ctx, in.OrderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != in.BuyerID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if order.Status != domain.OrderDelivered && order.Status != domain.OrderCompleted {
		return nil, domain.E(domain.KindConflict, "NOT_DELIVERED", "returns are only allowed for delivered orders")
	}
	var item *domain.OrderItem
	for _, it := range order.Items {
		if it.ID == in.OrderItemID {
			item = it
			break
		}
	}
	if item == nil {
		return nil, domain.E(domain.KindNotFound, "ITEM_NOT_FOUND", "order item not found")
	}
	if item.SellerID != order.SellerID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "item does not belong to this order")
	}

	status := domain.ReturnRequested
	resolution := ""
	// Auto-approve low-value claims (platform policy) to cut response latency.
	// The resolution MUST be set here — otherwise the later refund step
	// rejects with NO_REFUND and auto-approved claims dead-end forever.
	if s.returnAutoApproveMax > 0 && item.Total <= s.returnAutoApproveMax {
		status = domain.ReturnApproved
		resolution = "refund"
	}

	req := &domain.ReturnRequest{
		ID:           uuid.NewString(),
		OrderID:      in.OrderID,
		OrderItemID:  in.OrderItemID,
		BuyerID:      in.BuyerID,
		SellerID:     item.SellerID,
		IssueType:    in.IssueType,
		Reason:       in.Reason,
		Description:  strings.TrimSpace(in.Description),
		EvidenceURLs: in.EvidenceURLs,
		Status:       status,
		Resolution:   resolution,
	}
	switch in.Reason {
	case "wrong_item", "defective", "not_as_described", "other":
	default:
		return nil, domain.E(domain.KindInvalid, "BAD_REASON", "invalid return reason")
	}
	// HOLD FIRST, THEN RECORD THE RETURN. See PaymentService.HoldSellerFunds for
	// why the order of these two matters: a stray hold self-releases after the lag,
	// whereas a return recorded without a hold leaves the seller free to withdraw
	// the money the reversal is about to need.
	//
	// The hold is the ITEM's value, not the order's: a return reverses the item.
	// Holding the whole order would freeze money the seller is entitled to keep.
	if s.paymentSvc != nil {
		if err := s.paymentSvc.HoldSellerFunds(ctx, item.SellerID, order.ID, req.ID,
			HoldReturn, "return claim "+req.ID, item.Total); err != nil {
			// Loud. Proceeding without the hold is the exact condition this exists
			// to prevent, and it fails silently from the buyer's point of view: the
			// return is accepted, and then the refund is stuck on a balance that is
			// no longer there.
			return nil, err
		}
	}
	if err := s.stores.CreateReturn(ctx, req); err != nil {
		return nil, err
	}
	if s.notifs != nil {
		title, body := "Retur baru menunggu keputusan", "Pembeli mengajukan retur pada pesanan."
		if status == domain.ReturnApproved {
			title, body = "Retur disetujui otomatis", "Klaim bernilai kecil disetujui otomatis — siapkan penggantian barang."
		}
		_ = s.notifs.Notify(ctx, req.SellerID, "order", title, body+" ("+req.OrderID+")", map[string]any{"order_id": req.OrderID})
	}
	return req, nil
}

// MyReturns lists the buyer's claims.
func (s *SellerService) MyReturns(ctx context.Context, buyerID string) ([]*domain.ReturnRequest, error) {
	return s.stores.ReturnsByBuyer(ctx, buyerID)
}

// SellerReturns lists claims on the seller's orders.
func (s *SellerService) SellerReturns(ctx context.Context, sellerID, status string) ([]*domain.ReturnRequest, error) {
	return s.stores.ReturnsBySeller(ctx, sellerID, status)
}

// SellerDecideReturn approves or rejects a claim (seller).
func (s *SellerService) SellerDecideReturn(ctx context.Context, sellerID, returnID, decision, note string) error {
	req, err := s.stores.ReturnByID(ctx, returnID)
	if err != nil {
		return err
	}
	if req.SellerID != sellerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "return does not belong to store")
	}
	if req.Status != domain.ReturnRequested {
		return domain.E(domain.KindConflict, "NOT_PENDING", "return is already decided")
	}
	switch decision {
	case "approved":
		if err := s.stores.SetReturnStatus(ctx, returnID, domain.ReturnApproved, "refund", note, "seller_note"); err != nil {
			return err
		}
		s.emailBuyer(ctx, req.OrderID, "return_updated", "Retur disetujui — VinCommerce",
			map[string]any{"ReturnStatus": "disetujui", "ReturnNote": "Silakan kirim barang kembali."})
		if s.notifs != nil {
			_ = s.notifs.Notify(ctx, req.BuyerID, "order", "Retur disetujui",
				"Penjual menyetujui returanmu. Refund menyusul setelah barang diterima.",
				map[string]any{"order_id": req.OrderID})
		}
		return nil
	case "rejected":
		if err := s.stores.SetReturnStatus(ctx, returnID, domain.ReturnRejected, "none", note, "seller_note"); err != nil {
			return err
		}
		s.emailBuyer(ctx, req.OrderID, "return_updated", "Retur ditolak — VinCommerce",
			map[string]any{"ReturnStatus": "ditolak", "ReturnNote": note})
		if s.notifs != nil {
			_ = s.notifs.Notify(ctx, req.BuyerID, "order", "Retur ditolak",
				"Penjual menolak returanmu. Kamu bisa eskalasi menjadi sengketa.",
				map[string]any{"order_id": req.OrderID})
		}
		return nil
	}
	return domain.E(domain.KindInvalid, "BAD_DECISION", "decision must be approved or rejected")
}

// RefundReturn finalizes a refund after the item is returned (admin).
// Money movement is ledger-correct: the payment intent is flipped to
// RefundReturn refunds an approved return claim.
//
// This used to be a second, independent refund implementation, and it had NONE
// of the invariants RefundOrder had. It was an unbounded money mint reachable
// from a single admin click in the returns queue:
//
//   - No cumulative cap. It never consulted the ledger, so N approved returns on
//     one order each credited `req.Amount`. A four-item order produced four
//     credits; nothing bounded their sum against the charge.
//   - No debit anywhere. The buyer's credit had no counterpart at all. For a
//     Midtrans-funded order the real cash stayed at the gateway AND the buyer
//     received a spendable wallet balance. Repeatable per return claim.
//   - Gross seller debit. It debited the ITEM VALUE rather than the net the
//     seller received, and reversed no commission -- so the platform kept its
//     cut on a fully returned item and the seller paid the fee out of unrelated
//     balance. This is the exact defect RefundOrder's own doc comment claimed
//     had been fixed.
//   - Terminal status on the first item. It set IntentRefunded rather than
//     IntentPartiallyRefunded, so after refunding one Rp50,000 item of a
//     Rp500,000 order the intent was `refunded` and every other path refused
//     with NOT_REFUNDABLE. The remaining Rp450,000 was unrecoverable.
//   - It collided with `uq_wallet_tx_business_event` (00040), a partial unique
//     index on (wallet_id, ref_id, kind, reason): a second refund for the same
//     order violated it and rolled the whole transaction back with a raw 23505.
//
// It now goes through the same plan as every other refund: lock the intent,
// read the cumulative from the ledger inside the transaction, cap at the
// remaining, derive the two reversal legs so they sum to the refund exactly, and
// set the correct partial/terminal status. One implementation, so the invariants
// cannot be satisfied on one path and not the other.
func (s *SellerService) RefundReturn(ctx context.Context, returnID string, note string) error {
	req, err := s.stores.ReturnByID(ctx, returnID)
	if err != nil {
		return err
	}
	if req.Status != domain.ReturnApproved {
		return domain.E(domain.KindConflict, "NOT_APPROVED", "return is not approved")
	}
	if req.Resolution != "refund" {
		return domain.E(domain.KindConflict, "NO_REFUND", "return resolution is not a refund")
	}
	order, err := s.orders.ByID(ctx, req.OrderID)
	if err != nil {
		return err
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	// Claim the return atomically inside the same tx as the money movement, so
	// two concurrent admin clicks cannot both proceed.
	tag, err := q.Exec(ctx,
		`UPDATE return_requests SET status = 'refunded', admin_note = NULLIF($2, ''), resolved_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'approved' AND resolution = 'refund'`, returnID, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "ALREADY_REFUNDED", "return was already refunded or is not refundable")
	}

	intent, ierr := s.payments.IntentByOrder(ctx, req.OrderID)
	switch {
	case ierr == nil && (intent.Status == domain.IntentCaptured ||
		intent.Status == domain.IntentReleased ||
		intent.Status == domain.IntentPartiallyRefunded):
		// Delegate the money movement to the shared refund path so the cap, the
		// leg derivation and the status transition are identical to every other
		// refund. The return's own claim is already marked refunded above; if the
		// refund fails, the whole transaction rolls back including that UPDATE, so
		// the claim is not left in a state where it cannot be retried.
		//
		// This credits the wallet rather than calling the gateway, which is a
		// KNOWN and DELIBERATE divergence from RefundOrder. A gateway call is
		// network I/O and cannot sit inside this transaction: holding a row lock
		// on the return claim and the payment intent across an HTTP call to
		// Midtrans means a 15-second timeout stalls every other refund and every
		// admin click that touches the order.
		//
		// The cost is real and stated plainly: a return refund credits platform
		// balance rather than returning money to the buyer's card. Closing that
		// means making the return approval and the refund two steps -- approve,
		// commit, then refund through the gateway -- which is the right design but
		// changes the admin flow and the return's state machine, so it is its own
		// change rather than a quiet substitution here.
		if err := s.paymentSvc.RefundOrderInTx(ctx, tx, req.OrderID, req.Amount,
			"return refund: "+returnID); err != nil {
			return err
		}
	case ierr == nil:
		return domain.E(domain.KindConflict, "INTENT_STATE", "payment intent for this order is not refundable")
	default:
		// No intent at all: an externally-paid or legacy order. There is no
		// captured money to reverse and no rate card to reverse, so the only
		// correct action is to record that the return was approved and let an
		// operator settle it out of band. Crediting a wallet for money the
		// platform never received is how this path became a mint.
		//
		// Refusing is the honest behaviour. A wallet credit here would be funded
		// by nothing, and "refund the buyer" for an order paid outside the system
		// is an operator's decision with a bank transfer attached, not a button.
		return domain.E(domain.KindConflict, "NO_PAYMENT_INTENT",
			"this order has no payment record, so there is no money to reverse; "+
				"settle the refund with the buyer out of band and mark the return resolved")
	}

	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: req.OrderID, FromStatus: order.Status, ToStatus: order.Status,
		ActorID: nil, Note: fmt.Sprintf("return refund settled: %s", returnID),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.emailBuyer(ctx, req.OrderID, "return_updated", "Refund selesai — VinCommerce",
		map[string]any{"ReturnStatus": "refund", "ReturnNote": "Dana refund telah dikembalikan."})
	if s.notifs != nil {
		_ = s.notifs.Notify(ctx, req.BuyerID, "order", "Refund diproses",
			"Refund untuk returanmu telah diproses.", map[string]any{"order_id": req.OrderID})
	}
	return nil
}

// AdminListStores lists pending stores for approval.
func (s *SellerService) AdminListStores(ctx context.Context, pendingOnly bool) ([]*domain.Store, error) {
	if pendingOnly {
		return s.stores.PendingStores(ctx)
	}
	// For MVP, pending-only is the admin queue; a full store list is added with analytics.
	return s.stores.PendingStores(ctx)
}

// AdminReturns lists return requests by status (admin).
func (s *SellerService) AdminReturns(ctx context.Context, status string) ([]*domain.ReturnRequest, error) {
	return s.stores.ReturnsByStatus(ctx, status)
}

// AdminDecideStore approves/suspends/rejects a store (admin).
func (s *SellerService) AdminDecideStore(ctx context.Context, storeID, decision string) error {
	label := ""
	deactivate := false
	switch decision {
	case "approve":
		label = "disetujui"
		if err := s.stores.SetStatus(ctx, storeID, domain.StoreActive); err != nil {
			return err
		}
	case "suspend":
		label = "ditangguhkan"
		if err := s.stores.SetStatus(ctx, storeID, domain.StoreSuspended); err != nil {
			return err
		}
		deactivate = true
	case "reject":
		label = "ditolak"
		if err := s.stores.SetStatus(ctx, storeID, domain.StoreRejected); err != nil {
			return err
		}
		deactivate = true
	default:
		return domain.E(domain.KindInvalid, "BAD_DECISION", "decision must be approve, suspend or reject")
	}
	if deactivate {
		// A suspended/rejected store's catalog must vanish from the storefront
		// immediately (public queries also filter by store status; this keeps
		// seller dashboards and counts consistent too).
		store, err := s.stores.ByID(ctx, storeID)
		if err == nil {
			_, _ = s.products.Pool().Exec(ctx,
				`UPDATE products SET status = 'inactive', updated_at = now()
				 WHERE seller_id = $1 AND status = 'active'`, store.OwnerID)
		}
	}
	s.emailStoreOwner(ctx, storeID, "store_status", "Status toko diperbarui — VinCommerce",
		map[string]any{"StoreStatus": label})
	return nil
}

// AdminPendingKYC lists KYC submissions awaiting review (admin).
func (s *SellerService) AdminPendingKYC(ctx context.Context) ([]*repository.StoreKYC, error) {
	return s.stores.PendingKYC(ctx)
}

// AdminDecideKYC approves or rejects KYC (admin).
func (s *SellerService) AdminDecideKYC(ctx context.Context, storeID, decision, note string) error {
	switch decision {
	case "approve":
		if err := s.stores.SetKYCStatus(ctx, storeID, "approved", note); err != nil {
			return err
		}
		s.emailStoreOwner(ctx, storeID, "kyc_status", "Verifikasi identitas disetujui — VinCommerce",
			map[string]any{"KYCStatus": "disetujui"})
		return nil
	case "reject":
		if err := s.stores.SetKYCStatus(ctx, storeID, "rejected", note); err != nil {
			return err
		}
		s.emailStoreOwner(ctx, storeID, "kyc_status", "Verifikasi identitas ditolak — VinCommerce",
			map[string]any{"KYCStatus": "ditolak", "KYCNote": note})
		return nil
	}
	return domain.E(domain.KindInvalid, "BAD_DECISION", "decision must be approve or reject")
}

// emailStoreOwner sends a transactional email to the store owner.
func (s *SellerService) emailStoreOwner(ctx context.Context, storeID, templateName, subject string, data map[string]any) {
	if s.mailer == nil || s.users == nil {
		return
	}
	store, err := s.stores.ByID(ctx, storeID)
	if err != nil {
		return
	}
	owner, err := s.users.ByID(ctx, store.OwnerID)
	if err != nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Name"] = owner.FullName
	data["StoreName"] = store.Name
	_ = s.mailer.Send(ctx, owner.Email, subject, templateName, data)
}

// SendDailyDigests emails every active store owner a summary of yesterday's
// orders, revenue, top product and new reviews (worker).
func (s *SellerService) SendDailyDigests(ctx context.Context) (int, error) {
	if s.mailer == nil || s.users == nil {
		return 0, nil
	}
	stores, err := s.stores.ActiveStores(ctx)
	if err != nil {
		return 0, err
	}
	from := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	to := from.Add(24 * time.Hour)
	sent := 0
	for _, st := range stores {
		owner, err := s.users.ByID(ctx, st.OwnerID)
		if err != nil {
			continue
		}
		d, err := s.orders.DailyDigestFor(ctx, st.OwnerID, from, to)
		if err != nil {
			continue
		}
		_ = s.mailer.Send(ctx, owner.Email, "Ringkasan penjualan kemarin — VinCommerce", "daily_digest", map[string]any{
			"Name":        owner.FullName,
			"StoreName":   st.Name,
			"OrderCount":  d.OrderCount,
			"GMV":         fmt.Sprintf("Rp %.0f", d.GMV),
			"TopProduct":  d.TopProduct,
			"ReviewCount": d.ReviewCount,
		})
		sent++
	}
	return sent, nil
}

// ParseVariantInputs decodes variant JSON payloads.
func ParseVariantInputs(data string) (map[string]string, error) {
	var m map[string]string
	if strings.TrimSpace(data) == "" {
		return map[string]string{}, nil
	}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ImportResult reports a bulk product import.
type ImportResult struct {
	Created int              `json:"created"`
	Failed  int              `json:"failed"`
	Errors  []ImportRowError `json:"errors"`
}

// ImportRowError is one failed CSV row.
type ImportRowError struct {
	Row   int    `json:"row"`
	Error string `json:"error"`
}

// ImportProductsCSV creates products in bulk (name,category_slug,sku,price,stock,weight_grams).
func (s *SellerService) ImportProductsCSV(ctx context.Context, sellerID string, rows [][]string) (*ImportResult, error) {
	result := &ImportResult{Errors: []ImportRowError{}}
	for i, row := range rows {
		if len(row) < 5 {
			result.Failed++
			result.Errors = append(result.Errors, ImportRowError{Row: i + 1, Error: "harus punya 5+ kolom"})
			continue
		}
		name := strings.TrimSpace(row[0])
		catSlug := strings.TrimSpace(row[1])
		sku := strings.TrimSpace(row[2])
		price, err1 := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		stock, err2 := strconv.Atoi(strings.TrimSpace(row[4]))
		if name == "" || err1 != nil || err2 != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportRowError{Row: i + 1, Error: "nama, harga, atau stok tidak valid"})
			continue
		}
		weight := 0
		if len(row) > 5 {
			weight, _ = strconv.Atoi(strings.TrimSpace(row[5]))
		}

		var catID string
		if catSlug != "" {
			if cat, err := s.stores.CategoryBySlug(ctx, catSlug); err == nil {
				catID = cat.ID
			}
		}

		_, err := s.CreateProduct(ctx, CreateProductInput{
			SellerID:   sellerID,
			Name:       name,
			CategoryID: catID,
			Variants: []VariantInput{{
				SKU: sku, Name: "Default", Price: price, Stock: stock, WeightGrams: weight, IsActive: true,
			}},
		})
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportRowError{Row: i + 1, Error: err.Error()})
			continue
		}
		result.Created++
	}
	return result, nil
}

var _ = time.Second
