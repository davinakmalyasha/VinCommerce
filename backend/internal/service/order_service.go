package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/stream"
)

// orderNumberAlphabet is Crockford base32 minus the ambiguous glyphs
// (I, L, O, U), so a number read aloud or copied from a support chat
// screenshot cannot be mistyped into a wrong order.
const orderNumberAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newOrderNumber returns a customer-facing order reference with an
// UNGUESSABLE suffix: VC-YYYYMMDD-XXXXXXXX, where the 8 characters are
// base32 of 5 crypto/rand bytes (40 bits, ~1.1 trillion values per day-part).
//
// It used to be `VC-YYYYMMDD-%04d` over `nextval('order_number_seq')` — a
// global monotonic counter starting at 1000, shared by every order the platform
// has ever created. That number is the primary key a buyer types into
// `GET /orders/tracking/{number}`, so its predictability turned any missing
// ownership check on that endpoint into a platform-wide enumeration: a loop over
// four digits walks the entire order table. The day prefix did not help; it
// narrows the range rather than hiding it, and the counter is reset by nothing
// so a determined scan covers every date.
//
// Keeping the human-readable prefix is worth it: support needs a date the
// buyer can read, and the suffix is what has to carry the entropy. 40 bits is
// chosen so that even an endpoint with NO rate limit and NO ownership check
// would be infeasible to enumerate, rather than relying on the two controls
// above it to both be present and correct.
func newOrderNumber(now time.Time) (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice, but a predictable fallback
		// would silently reintroduce the exact bug this fixes. Fail the
		// checkout instead.
		return "", domain.Wrap(domain.KindInternal, "ORDER_NUMBER_FAILED", "could not generate order number", err)
	}
	var sb strings.Builder
	sb.Grow(len("VC-20060102-") + 8)
	sb.WriteString("VC-")
	sb.WriteString(now.UTC().Format("20060102"))
	sb.WriteByte('-')
	// 5 bytes = 40 bits, and base32 consumes exactly 5 bits per symbol, so the
	// suffix is exactly 8 symbols with no padding.
	//
	// Pack the bytes into a 40-bit value and peel off five bits at a time from
	// the top. The obvious-looking alternative -- two symbols per byte, one from
	// `x>>3` and one from `x&0x07` -- is wrong in a way that is easy to miss:
	// it emits TEN symbols, and the low half of each pair has only 8 possible
	// values instead of 32. Total entropy is the same 40 bits, but the alphabet
	// is not uniform across positions, so the format is undocumented and a
	// caller that assumes "8 characters, 32 options each" mis-models it.
	var bits uint64
	for _, x := range b {
		bits = bits<<8 | uint64(x)
	}
	for i := 0; i < 8; i++ {
		sb.WriteByte(orderNumberAlphabet[(bits>>uint(35-5*i))&0x1F])
	}
	return sb.String(), nil
}

// OrderService implements cart, quote, checkout and order lifecycle.
type OrderService struct {
	carts         *repository.CartRepository
	orders        *repository.OrderRepository
	addresses     *repository.AddressRepository
	users         *repository.UserRepository
	stores        *repository.StoreRepository
	loyalty       *repository.LoyaltyRepository
	wishlist      *repository.WishlistRepository
	broker        *stream.Broker
	payments      *PaymentService
	notifications *NotificationService
	mailer        *mail.Client
	webURL        string
	insurancePct  float64
	logger        *slog.Logger
}

// NewOrderService creates an OrderService.
func NewOrderService(carts *repository.CartRepository, orders *repository.OrderRepository, addresses *repository.AddressRepository) *OrderService {
	return &OrderService{carts: carts, orders: orders, addresses: addresses}
}

// SetStores enables free-shipping thresholds in quotes.
func (s *OrderService) SetStores(st *repository.StoreRepository) { s.stores = st }

// SetWishlist enables move-to-wishlist from the cart.
func (s *OrderService) SetWishlist(w *repository.WishlistRepository) { s.wishlist = w }

// SetLoyalty awards points when orders complete.
func (s *OrderService) SetLoyalty(l *repository.LoyaltyRepository) { s.loyalty = l }

// awardPoints credits 1 point per Rp 1000 spent on a completed order.
func (s *OrderService) awardPoints(ctx context.Context, orderID string) {
	if s.loyalty == nil {
		return
	}
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return
	}
	points := int(o.TotalAmount / 1000)
	if points > 0 {
		_ = s.loyalty.Add(ctx, o.BuyerID, points, "order_complete", o.ID)
	}
}

// SetUsers enables buyer lookup for transactional emails.
func (s *OrderService) SetUsers(u *repository.UserRepository) { s.users = u }

// SetBroker attaches the realtime event broker (optional).
func (s *OrderService) SetBroker(b *stream.Broker) { s.broker = b }

// SetPaymentService enables escrow release on order completion.
func (s *OrderService) SetPaymentService(p *PaymentService) { s.payments = p }

// SetNotificationService persists order events as in-app notifications.
func (s *OrderService) SetNotificationService(n *NotificationService) { s.notifications = n }

// SetMailer enables transactional order emails.
func (s *OrderService) SetMailer(m *mail.Client, webURL string) { s.mailer = m; s.webURL = webURL }

// SetLogger attaches a structured logger. Required for the post-commit paths
// (loyalty debit, email failures) where the work is already durable and the
// only remaining obligation is to make the failure visible.
func (s *OrderService) SetLogger(l *slog.Logger) { s.logger = l }

func (s *OrderService) emailFor(ctx context.Context, orderID string, templateName, subject string, data map[string]any) {
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

// RecoverAbandonedCarts emails users with stale carts and raises an in-app
// notification pointing back to the cart (worker).
func (s *OrderService) RecoverAbandonedCarts(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	if s.mailer == nil {
		return 0, nil
	}
	carts, err := s.carts.AbandonedCarts(ctx, time.Now().UTC().Add(-olderThan), limit)
	if err != nil {
		return 0, err
	}
	if len(carts) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(carts))
	for _, c := range carts {
		ids = append(ids, c.ID)
		_ = s.mailer.Send(ctx, c.Email, "Keranjangmu menunggu! 🛒", "cart_abandoned", map[string]any{
			"Name":      c.FullName,
			"ItemCount": c.ItemCount,
			"ItemNames": c.ItemNames,
			"CartURL":   s.webURL + "/cart",
		})
		if s.notifications != nil {
			_ = s.notifications.Notify(ctx, c.UserID, "cart_recovery", "Keranjangmu menunggu! 🛒",
				fmt.Sprintf("Kamu punya %d barang di keranjang (%s). Selesaikan sebelum kehabisan!", c.ItemCount, c.ItemNames),
				map[string]any{"cart_url": "/cart"})
		}
	}
	if err := s.carts.MarkRecovered(ctx, ids); err != nil {
		return 0, err
	}
	return len(carts), nil
}

// ConfirmDelivery marks a shipped order as delivered (buyer). COD orders capture payment here.
func (s *OrderService) ConfirmDelivery(ctx context.Context, orderID, buyerID string) error {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.BuyerID != buyerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if order.Status != domain.OrderShipped {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION", "order is not in shipped state")
	}
	if err := s.orders.TransitionOrder(ctx, orderID, domain.OrderShipped, domain.OrderDelivered, buyerID); err != nil {
		return err
	}
	s.publishOrderEvent(ctx, orderID, domain.OrderShipped, domain.OrderDelivered)
	if s.payments != nil {
		if err := s.payments.CaptureCOD(ctx, orderID); err != nil {
			return err
		}
	}
	return nil
}

// CompleteOrder finalizes a delivered order and releases escrow to the seller (buyer confirm).
func (s *OrderService) CompleteOrder(ctx context.Context, orderID, buyerID string) error {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.BuyerID != buyerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if order.Status != domain.OrderDelivered {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION", "order is not in delivered state")
	}
	if err := s.orders.TransitionOrder(ctx, orderID, domain.OrderDelivered, domain.OrderCompleted, buyerID); err != nil {
		return err
	}
	s.publishOrderEvent(ctx, orderID, domain.OrderDelivered, domain.OrderCompleted)
	s.awardPoints(ctx, orderID)
	if s.payments != nil {
		if err := s.payments.ReleaseEscrow(ctx, orderID); err != nil {
			return err
		}
	}
	return nil
}

// ListAddresses returns the user's address book.
func (s *OrderService) ListAddresses(ctx context.Context, userID string) ([]*domain.Address, error) {
	return s.addresses.List(ctx, userID)
}

// CreateAddress adds an address book entry.
func (s *OrderService) CreateAddress(ctx context.Context, userID string, a *domain.Address) (*domain.Address, error) {
	a.ID = uuid.NewString()
	a.UserID = userID
	if err := s.addresses.Create(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// DefaultAddress returns the user's default address.
func (s *OrderService) DefaultAddress(ctx context.Context, userID string) (*domain.Address, error) {
	return s.addresses.Default(ctx, userID)
}

// DeleteAddress removes an address.
func (s *OrderService) DeleteAddress(ctx context.Context, userID, addressID string) error {
	return s.addresses.Delete(ctx, userID, addressID)
}

// UpdateAddress edits an owned address.
func (s *OrderService) UpdateAddress(ctx context.Context, userID string, a *domain.Address) error {
	return s.addresses.Update(ctx, userID, a)
}

// ReservationHold is how long stock stays reserved for an unpaid order.
const ReservationHold = 30 * time.Minute

// SetInsurancePct enables the optional shipping-insurance upsell
// (percentage of each seller bundle's subtotal). 0 disables it.
func (s *OrderService) SetInsurancePct(pct float64) { s.insurancePct = pct }

// --- Cart ---

// CartFor resolves the correct cart for a user or guest session.
func (s *OrderService) CartFor(ctx context.Context, userID, sessionKey string) (*domain.Cart, error) {
	if userID != "" {
		return s.carts.EnsureActiveByUser(ctx, userID)
	}
	if sessionKey == "" {
		sessionKey = uuid.NewString()
	}
	return s.carts.EnsureActiveBySession(ctx, sessionKey)
}

// AddToCart validates stock and adds an item.
func (s *OrderService) AddToCart(ctx context.Context, cartID, variantID string, quantity int) (*domain.Cart, []*domain.CartLine, error) {
	if quantity < 1 {
		return nil, nil, domain.E(domain.KindInvalid, "BAD_QUANTITY", "quantity must be at least 1")
	}
	var stock int
	var active bool
	err := s.carts.EnsureVariant(ctx, variantID, &stock, &active)
	if err != nil {
		return nil, nil, err
	}
	if !active {
		return nil, nil, domain.E(domain.KindConflict, "VARIANT_INACTIVE", "this variant is no longer available")
	}
	if stock < quantity {
		return nil, nil, domain.E(domain.KindConflict, "INSUFFICIENT_STOCK", "not enough stock available")
	}
	if err := s.carts.AddItem(ctx, cartID, variantID, quantity); err != nil {
		return nil, nil, err
	}
	return s.carts.GetWithItems(ctx, cartID)
}

// UpdateCartItem sets the quantity of a line.
func (s *OrderService) UpdateCartItem(ctx context.Context, cartID, variantID string, quantity int) (*domain.Cart, []*domain.CartLine, error) {
	if quantity < 0 {
		return nil, nil, domain.E(domain.KindInvalid, "BAD_QUANTITY", "quantity must be positive")
	}
	if err := s.carts.UpdateItem(ctx, cartID, variantID, quantity); err != nil {
		return nil, nil, err
	}
	return s.carts.GetWithItems(ctx, cartID)
}

// RemoveFromCart deletes a line.
func (s *OrderService) RemoveFromCart(ctx context.Context, cartID, variantID string) (*domain.Cart, []*domain.CartLine, error) {
	if err := s.carts.RemoveItem(ctx, cartID, variantID); err != nil {
		return nil, nil, err
	}
	return s.carts.GetWithItems(ctx, cartID)
}

// BulkRemoveFromCart deletes multiple lines.
func (s *OrderService) BulkRemoveFromCart(ctx context.Context, cartID string, variantIDs []string) (*domain.Cart, []*domain.CartLine, error) {
	if err := s.carts.RemoveItems(ctx, cartID, variantIDs); err != nil {
		return nil, nil, err
	}
	return s.carts.GetWithItems(ctx, cartID)
}

// BulkMoveToWishlist moves cart lines into the user's wishlist atomically.
func (s *OrderService) BulkMoveToWishlist(ctx context.Context, userID, cartID string, variantIDs []string) (*domain.Cart, []*domain.CartLine, error) {
	if s.wishlist == nil {
		return nil, nil, domain.E(domain.KindConflict, "UNAVAILABLE", "wishlist unavailable")
	}
	if err := s.carts.MoveToWishlist(ctx, userID, cartID, variantIDs); err != nil {
		return nil, nil, err
	}
	return s.carts.GetWithItems(ctx, cartID)
}

// GetCart returns cart with lines.
func (s *OrderService) GetCart(ctx context.Context, cartID string) (*domain.Cart, []*domain.CartLine, error) {
	return s.carts.GetWithItems(ctx, cartID)
}

// MergeCart moves a guest cart into the user's cart.
func (s *OrderService) MergeCart(ctx context.Context, userID, guestCartID string) error {
	userCart, err := s.carts.EnsureActiveByUser(ctx, userID)
	if err != nil {
		return err
	}
	return s.carts.Merge(ctx, guestCartID, userCart.ID)
}

// FreeShippingStatus describes one seller bundle's progress toward that
// store's free-shipping threshold (cart upsell banner).
type FreeShippingStatus struct {
	SellerID  string   `json:"seller_id"`
	Name      string   `json:"name"`
	Subtotal  float64  `json:"subtotal"`
	Threshold *float64 `json:"threshold,omitempty"`
}

// FreeShippingStatus computes per-seller progress toward free shipping.
func (s *OrderService) FreeShippingStatus(ctx context.Context, userID, sessionKey string) ([]*FreeShippingStatus, error) {
	cart, err := s.CartFor(ctx, userID, sessionKey)
	if err != nil {
		return nil, err
	}
	_, lines, err := s.carts.GetWithItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	bundles := map[string]float64{}
	names := map[string]string{}
	for _, l := range lines {
		bundles[l.SellerID] += l.Subtotal
		names[l.SellerID] = l.SellerName
	}
	ids := make([]string, 0, len(bundles))
	for sid := range bundles {
		ids = append(ids, sid)
	}
	sort.Strings(ids)

	out := []*FreeShippingStatus{}
	for _, sid := range ids {
		th, err := s.stores.FreeShippingThreshold(ctx, sid)
		if err != nil {
			return nil, err
		}
		out = append(out, &FreeShippingStatus{
			SellerID: sid, Name: names[sid], Subtotal: bundles[sid], Threshold: th,
		})
	}
	return out, nil
}

// --- Checkout ---

// Quote computes the checkout breakdown without placing the order.
type Quote struct {
	Lines          []*domain.CartLine `json:"lines"`
	Subtotal       float64            `json:"subtotal"`
	DiscountAmount float64            `json:"discount_amount"`
	CouponCode     string             `json:"coupon_code,omitempty"`
	Shipping       []*ShippingQuote   `json:"shipping"`
	Total          float64            `json:"total"`

	InsuranceAvailable bool    `json:"insurance_available"`
	InsuranceSelected  bool    `json:"insurance_selected"`
	InsuranceFee       float64 `json:"insurance_fee"`
	PointsDiscount     float64 `json:"points_discount,omitempty"`
	PointsRedeemed     int     `json:"points_redeemed,omitempty"`

	discountBySeller map[string]float64
}

// DiscountFor returns the discount allocated to one seller bundle.
func (q *Quote) DiscountFor(sid string) float64 { return q.discountBySeller[sid] }

// ShippingQuote is per-method fee for one seller bundle.
type ShippingQuote struct {
	MethodID     string  `json:"method_id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	BaseFee      float64 `json:"base_fee"`
	PerKgFee     float64 `json:"per_kg_fee"`
	WeightKg     int     `json:"weight_kg"`
	Fee          float64 `json:"fee"`
	InsuranceFee float64 `json:"insurance_fee"`
	MinDays      int     `json:"min_days"`
	MaxDays      int     `json:"max_days"`
}

// LoyaltyPointValueIDR is the rupiah value of one loyalty point at checkout.
const LoyaltyPointValueIDR = 100

// QuoteCheckout validates items, applies the coupon and computes shipping.
func (s *OrderService) QuoteCheckout(ctx context.Context, userID, cartID, couponCode, shippingMethodCode string, insurance bool, pointsToRedeem int) (*Quote, error) {
	_, lines, err := s.carts.GetWithItems(ctx, cartID)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, domain.E(domain.KindInvalid, "EMPTY_CART", "cart is empty")
	}

	subtotal := 0.0
	for _, l := range lines {
		if !l.IsActive {
			return nil, domain.E(domain.KindConflict, "VARIANT_INACTIVE", l.ProductName+" is no longer available")
		}
		if l.Stock < l.Quantity {
			return nil, domain.E(domain.KindConflict, "INSUFFICIENT_STOCK",
				fmt.Sprintf("%s: only %d left in stock", l.ProductName, l.Stock))
		}
		// apply the active flash-sale price (if any) so the order matches the sale price
		if s.wishlist != nil {
			if sp, err := s.wishlist.FlashSalePriceFor(ctx, l.VariantID); err == nil && sp != nil && *sp < l.Price {
				l.Price = *sp
				l.Subtotal = *sp * float64(l.Quantity)
			}
		}
		subtotal += l.Subtotal
	}

	quote := &Quote{Lines: lines, Subtotal: subtotal, discountBySeller: map[string]float64{}}

	// coupon: platform coupons apply globally; store coupons only to their seller's bundle
	var coupon *domain.Coupon
	if couponCode != "" {
		c, _, err := s.validCoupon(ctx, userID, couponCode, subtotal)
		if err != nil {
			return nil, err
		}
		coupon = c
		quote.CouponCode = coupon.Code
		if coupon.SellerID == nil {
			quote.DiscountAmount = couponDiscount(coupon, subtotal)
		}
	}

	// loyalty points redemption: LoyaltyPointValueIDR per point, capped so the
	// merchandising total never goes negative.
	if pointsToRedeem > 0 && s.loyalty != nil {
		balance, err := s.loyalty.Balance(ctx, userID)
		if err != nil {
			return nil, err
		}
		if pointsToRedeem > balance {
			return nil, domain.E(domain.KindConflict, "INSUFFICIENT_POINTS", "poin tidak cukup")
		}
		maxDiscount := subtotal - quote.DiscountAmount
		d := float64(pointsToRedeem) * LoyaltyPointValueIDR
		if d > maxDiscount {
			d = maxDiscount
			pointsToRedeem = int(math.Ceil(d / LoyaltyPointValueIDR))
		}
		quote.PointsRedeemed = pointsToRedeem
		quote.PointsDiscount = d
	}

	// shipping: bundle lines by seller, quote per seller with the chosen method
	methods, err := s.orders.ShippingMethods(ctx)
	if err != nil {
		return nil, err
	}

	sellerBundles := map[string][]*domain.CartLine{}
	for _, l := range lines {
		sellerBundles[l.SellerID] = append(sellerBundles[l.SellerID], l)
	}

	var method *domain.ShippingMethod
	for _, m := range methods {
		if shippingMethodCode == "" || m.Code == shippingMethodCode {
			method = m
			break
		}
	}
	if method == nil {
		return nil, domain.E(domain.KindInvalid, "SHIPPING_METHOD", "unknown shipping method")
	}

	total := subtotal - quote.DiscountAmount - quote.PointsDiscount
	sellerIDs := make([]string, 0, len(sellerBundles))
	for sid := range sellerBundles {
		sellerIDs = append(sellerIDs, sid)
	}
	sort.Strings(sellerIDs)

	// Allocate platform-level discounts (global coupon + points) across seller
	// bundles proportionally to their subtotal share, so each sub-order carries
	// its fair share instead of duplicating the full discount.
	bundleSubtotals := map[string]float64{}
	for _, sid := range sellerIDs {
		s2 := 0.0
		for _, l := range sellerBundles[sid] {
			s2 += l.Subtotal
		}
		bundleSubtotals[sid] = s2
	}
	allocateGlobal := func(amount float64) {
		if amount <= 0 || len(sellerIDs) == 0 {
			return
		}
		var sumAll float64
		for _, sid := range sellerIDs {
			sumAll += bundleSubtotals[sid]
		}
		if sumAll <= 0 {
			return
		}
		assigned := 0.0
		for i, sid := range sellerIDs {
			var share float64
			if i == len(sellerIDs)-1 {
				share = amount - assigned // remainder lands on the last bundle
			} else {
				share = math.Round(amount*bundleSubtotals[sid]/sumAll*100) / 100
				assigned += share
			}
			quote.discountBySeller[sid] += share
		}
	}
	if coupon != nil && coupon.SellerID == nil {
		allocateGlobal(quote.DiscountAmount)
	}
	allocateGlobal(quote.PointsDiscount)

	quote.InsuranceAvailable = s.insurancePct > 0
	quote.InsuranceSelected = insurance && quote.InsuranceAvailable

	for _, sid := range sellerIDs {
		bundleSubtotal := 0.0
		weightKg := 0
		for _, l := range sellerBundles[sid] {
			bundleSubtotal += l.Subtotal
			weightKg += (l.WeightGrams * l.Quantity) / 1000
		}
		// store coupon discount scoped to this seller
		if coupon != nil && coupon.SellerID != nil && *coupon.SellerID == sid {
			d := couponDiscount(coupon, bundleSubtotal)
			quote.DiscountAmount += d
			total -= d
			quote.discountBySeller[sid] += d
		}
		// free shipping threshold
		freeThreshold, err := s.stores.FreeShippingThreshold(ctx, sid)
		if err != nil {
			return nil, err
		}
		if freeThreshold != nil && bundleSubtotal >= *freeThreshold {
			quote.Shipping = append(quote.Shipping, &ShippingQuote{
				MethodID: method.ID, Code: method.Code, Name: method.Name,
				BaseFee: method.BaseFee, PerKgFee: method.PerKgFee,
				WeightKg: weightKg, Fee: 0, MinDays: method.MinDays, MaxDays: method.MaxDays,
			})
		} else {
			fee := method.BaseFee + float64(weightKg)*method.PerKgFee
			quote.Shipping = append(quote.Shipping, &ShippingQuote{
				MethodID: method.ID, Code: method.Code, Name: method.Name,
				BaseFee: method.BaseFee, PerKgFee: method.PerKgFee,
				WeightKg: weightKg, Fee: fee, MinDays: method.MinDays, MaxDays: method.MaxDays,
			})
			total += fee
		}
		if quote.InsuranceSelected {
			sf := math.Round(bundleSubtotal*s.insurancePct) / 100
			quote.Shipping[len(quote.Shipping)-1].InsuranceFee = sf
			quote.InsuranceFee += sf
			total += sf
		}
	}
	quote.Total = total
	return quote, nil
}

// PlaceOrderInput for checkout.
type PlaceOrderInput struct {
	UserID             string
	CartID             string
	AddressID          string
	Address            *domain.Address
	AddressesBySeller  map[string]*domain.Address
	CouponCode         string
	ShippingMethodCode string
	Insurance          bool
	PointsToRedeem     int
	Notes              string
	IdempotencyKey     string
}

// ResolveAddress picks the shipping address: explicit ID (address book) or inline payload.
func (s *OrderService) ResolveAddress(ctx context.Context, in PlaceOrderInput) (*domain.Address, error) {
	if in.AddressID != "" {
		addr, err := s.addresses.ByID(ctx, in.AddressID)
		if err != nil {
			return nil, err
		}
		if addr.UserID != in.UserID {
			return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "address does not belong to user")
		}
		return addr, nil
	}
	if in.Address == nil {
		return nil, domain.E(domain.KindInvalid, "ADDRESS_REQUIRED", "shipping address is required")
	}
	return in.Address, nil
}

// PlacedOrder is the checkout result: one order per seller.
type PlacedOrder struct {
	Orders     []*domain.Order `json:"orders"`
	GrandTotal float64         `json:"grand_total"`
}

// BuyNowInput places a single-variant order straight from a product page.
type BuyNowInput struct {
	UserID             string
	VariantID          string
	Quantity           int
	AddressID          string
	Address            *domain.Address
	ShippingMethodCode string
	Notes              string
	IdempotencyKey     string
}

// BuyNow implements one-click checkout: an ephemeral per-user cart carries
// the single line through the normal quote+place path, so flash-sale pricing,
// stock validation, reservations and seller splitting are all reused
// untouched — and the buyer's persistent cart is never modified.
func (s *OrderService) BuyNow(ctx context.Context, in BuyNowInput) (*PlacedOrder, error) {
	if in.Quantity < 1 {
		return nil, domain.E(domain.KindInvalid, "BAD_QUANTITY", "quantity must be at least 1")
	}
	cart, err := s.carts.EnsureActiveBySession(ctx, "buynow-"+in.UserID)
	if err != nil {
		return nil, err
	}
	// reset the scratch cart to exactly this variant
	if _, lines, lerr := s.carts.GetWithItems(ctx, cart.ID); lerr == nil && len(lines) > 0 {
		ids := make([]string, 0, len(lines))
		for _, l := range lines {
			ids = append(ids, l.VariantID)
		}
		if err := s.carts.RemoveItems(ctx, cart.ID, ids); err != nil {
			return nil, err
		}
	}
	if _, _, err := s.AddToCart(ctx, cart.ID, in.VariantID, in.Quantity); err != nil {
		return nil, err
	}
	return s.PlaceOrder(ctx, PlaceOrderInput{
		UserID:             in.UserID,
		CartID:             cart.ID,
		AddressID:          in.AddressID,
		Address:            in.Address,
		ShippingMethodCode: in.ShippingMethodCode,
		Notes:              in.Notes,
		IdempotencyKey:     in.IdempotencyKey,
	})
}

// PlaceOrder executes checkout atomically: reserves stock, splits by seller, persists.
func (s *OrderService) PlaceOrder(ctx context.Context, in PlaceOrderInput) (*PlacedOrder, error) {
	address, err := s.ResolveAddress(ctx, in)
	if err != nil {
		return nil, err
	}
	if in.CartID == "" {
		return nil, domain.E(domain.KindInvalid, "CART_REQUIRED", "cart is required")
	}
	quote, err := s.QuoteCheckout(ctx, in.UserID, in.CartID, in.CouponCode, in.ShippingMethodCode, in.Insurance, in.PointsToRedeem)
	if err != nil {
		return nil, err
	}

	// group lines by seller
	sellerBundles := map[string][]*domain.CartLine{}
	for _, l := range quote.Lines {
		sellerBundles[l.SellerID] = append(sellerBundles[l.SellerID], l)
	}
	sellerIDs := make([]string, 0, len(sellerBundles))
	for sid := range sellerBundles {
		sellerIDs = append(sellerIDs, sid)
	}
	sort.Strings(sellerIDs)

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	placed := &PlacedOrder{}
	shipIdx := 0
	for _, sid := range sellerIDs {
		orderID := uuid.NewString()
		orderNumber, err := newOrderNumber(time.Now())
		if err != nil {
			return nil, err
		}
		bundle := sellerBundles[sid]

		subtotal := 0.0
		weightKg := 0
		for _, l := range bundle {
			subtotal += l.Subtotal
			weightKg += (l.WeightGrams * l.Quantity) / 1000
		}

		discount := quote.DiscountFor(sid)
		shippingFee := quote.Shipping[shipIdx].Fee
		insuranceFee := quote.Shipping[shipIdx].InsuranceFee
		shipIdx++

		order := &domain.Order{
			ID:              orderID,
			OrderNumber:     orderNumber,
			BuyerID:         in.UserID,
			SellerID:        sid,
			Status:          domain.OrderPending,
			Currency:        "IDR",
			Subtotal:        subtotal,
			DiscountAmount:  discount,
			ShippingFee:     shippingFee,
			InsuranceFee:    insuranceFee,
			TotalAmount:     subtotal - discount + shippingFee + insuranceFee,
			PaymentStatus:   domain.PaymentUnpaid,
			CouponCode:      quote.CouponCode,
			ShippingAddress: addressMap(orderAddress(address, in.AddressesBySeller, sid)),
			ShippingMethod:  quote.Shipping[shipIdx-1].Name,
			Notes:           in.Notes,
		}
		if err := tx.CreateOrder(ctx, order); err != nil {
			return nil, err
		}
		if err := tx.AddEvent(ctx, &domain.OrderEvent{
			OrderID: order.ID, FromStatus: "", ToStatus: domain.OrderPending,
			ActorID: &in.UserID, Note: "order placed",
		}); err != nil {
			return nil, err
		}

		for _, l := range bundle {
			item := &domain.OrderItem{
				ID:          uuid.NewString(),
				OrderID:     order.ID,
				ProductID:   l.ProductID,
				VariantID:   l.VariantID,
				SellerID:    sid,
				ProductName: l.ProductName,
				VariantName: l.VariantName,
				SKU:         l.SKU,
				UnitPrice:   l.Price,
				Quantity:    l.Quantity,
				WeightGrams: l.WeightGrams,
				Total:       l.Subtotal,
				ImageURL:    l.ImageURL,
				Status:      domain.OrderPending,
			}
			if err := tx.CreateItem(ctx, item); err != nil {
				return nil, err
			}
			if err := tx.ReserveStock(ctx, l.VariantID, order.ID, l.Quantity, ReservationHold); err != nil {
				return nil, err
			}
		}
		placed.Orders = append(placed.Orders, order)
		placed.GrandTotal += order.TotalAmount
	}

	if quote.CouponCode != "" {
		coupon, err := s.orders.CouponByCode(ctx, quote.CouponCode)
		if err != nil {
			return nil, err
		}
		if err := tx.IncrementCouponUsage(ctx, coupon.ID, in.UserID, placed.Orders[0].ID); err != nil {
			return nil, err
		}
	}

	if err := tx.ClearCart(ctx, in.CartID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Debit redeemed points after the order commits.
	//
	// The order is already durable at this point, so a failed debit cannot be
	// rolled back — but it MUST NOT be silent. The previous code used
	// fmt.Println, which on a structured-logging service means the failure
	// appears on stdout as plain text, is invisible to every log index and
	// alert rule, and leaves the buyer holding a discount they never paid for
	// in points. Escalate loudly so it becomes an alertable event.
	if quote.PointsRedeemed > 0 && s.loyalty != nil {
		refID := ""
		if len(placed.Orders) > 0 {
			refID = placed.Orders[0].ID
		}
		if err := s.loyalty.Spend(ctx, in.UserID, quote.PointsRedeemed, "redemption", refID); err != nil {
			if s.logger != nil {
				s.logger.Error("loyalty redemption debit failed after order commit; "+
					"buyer holds an uncharged discount and needs manual reconciliation",
					"user_id", in.UserID, "order_id", refID,
					"points", quote.PointsRedeemed, "error", err.Error())
			}
			// Still emit the order-confirmation email: the order exists and the
			// buyer is waiting. Swallowing the error here (rather than
			// returning it) is deliberate — returning would tell the buyer the
			// order failed when it was in fact placed.
		}
	}

	for _, o := range placed.Orders {
		s.emailFor(ctx, o.ID, "order_confirmed", "Pesanan dikonfirmasi — VinCommerce",
			map[string]any{"Total": fmt.Sprintf("Rp %.0f", o.TotalAmount)})
	}
	return placed, nil
}

// --- lifecycle ---

// Transition applies a validated state change and records the event.
func (s *OrderService) Transition(ctx context.Context, orderID, from, to string, actorID *string, note string) error {
	if !domain.CanTransition(from, to) {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION",
			fmt.Sprintf("cannot move order from %s to %s", from, to))
	}
	if err := s.orders.Transition(ctx, orderID, from, to, actorID, note); err != nil {
		return err
	}
	if err := s.orders.SetStatus(ctx, orderID, to); err != nil {
		return err
	}
	s.publishOrderEvent(ctx, orderID, from, to)
	return nil
}

// ConfirmExternalPayment records an off-platform payment (buyer pays outside
// the app, e.g. bank transfer), moving the order from pending to paid.
// The reported amount must match the order total — a mismatching or absent
// amount is rejected so an order can never be marked paid for less than owed.
func (s *OrderService) ConfirmExternalPayment(ctx context.Context, orderID, buyerID, reference string, amount float64, paidAt time.Time) error {
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	if o.BuyerID != buyerID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	if o.Status != domain.OrderPending {
		return domain.E(domain.KindConflict, "NOT_PENDING", "only unpaid orders can be confirmed")
	}
	if strings.TrimSpace(reference) == "" {
		return domain.E(domain.KindInvalid, "REF_REQUIRED", "external payment reference is required")
	}
	if len(strings.TrimSpace(reference)) > 100 {
		return domain.E(domain.KindInvalid, "REF_TOO_LONG", "external payment reference is too long")
	}
	if amount <= 0 {
		return domain.E(domain.KindInvalid, "BAD_AMOUNT", "amount must be positive")
	}
	if absDiff(amount, o.TotalAmount) > 1.0 {
		return domain.E(domain.KindConflict, "AMOUNT_MISMATCH",
			fmt.Sprintf("reported amount Rp %.2f does not match order total Rp %.2f", amount, o.TotalAmount))
	}
	if paidAt.IsZero() {
		paidAt = time.Now().UTC()
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := tx.MarkExternalPaid(ctx, orderID, strings.TrimSpace(reference), paidAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.publishOrderEvent(ctx, orderID, domain.OrderPending, domain.OrderPaid)
	if s.notifications != nil {
		_ = s.notifications.Notify(ctx, o.BuyerID, "payment", "Pembayaran tercatat ✅",
			"Pembayaran untuk pesanan "+o.OrderNumber+" sudah kami catat", map[string]any{"order_id": orderID})
	}
	s.emailFor(ctx, orderID, "order_paid", "Pembayaran diterima — VinCommerce",
		map[string]any{"Total": fmt.Sprintf("Rp %.0f", o.TotalAmount)})
	return nil
}

// CancelOrder cancels an order and releases its stock.
//
// A paid order is NOT silently cancellable. The old version accepted
// OrderPaid, restocked the inventory, and never touched the payment intent:
// for a wallet payment the buyer's money had already left their balance with
// no return path, and for a gateway payment it sat at the provider with no
// return path. The seller also lost the sale while the stock went back on sale.
//
// Money movement is the payments service's job, so a paid order is refused
// here with an explicit reason and the caller refunds first. That also closes
// the release+refund mint: ReleaseEscrow and RefundOrder now both reject a
// cancelled order.
func (s *OrderService) CancelOrder(ctx context.Context, orderID string, actorID *string, note string) error {
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return err
	}
	if o.Status != domain.OrderPending && o.Status != domain.OrderPaid {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION", "only pending or paid orders can be cancelled")
	}

	// A paid order has captured money. Require an explicit refund first rather
	// than orphaning the funds.
	//
	// This MUST fail closed. The first version treated any error from the
	// intent lookup the same as "there is no escrow", so a slow or saturated
	// pool (or a statement timeout) silently disabled the guard and let a
	// paid order be cancelled with its money already gone — the exact bug the
	// guard exists to prevent, reachable by making Postgres slow.
	if o.PaymentStatus == domain.PaymentPaid && s.payments != nil {
		intent, ierr := s.payments.IntentByOrder(ctx, orderID)
		switch {
		case ierr == nil && (intent.Status == domain.IntentCaptured ||
			intent.Status == domain.IntentReleased ||
			intent.Status == domain.IntentPartiallyRefunded):
			return domain.E(domain.KindConflict, "REFUND_BEFORE_CANCEL",
				"payment is already captured; refund the order before cancelling it")

		case ierr != nil && !errors.Is(ierr, domain.ErrNotFound):
			// Not "no such intent" — a real failure. Refuse: the cost of a
			// spurious refusal is one retry, the cost of a false "safe" is the
			// buyer's money.
			return ierr

		default:
			// Genuinely no intent (an externally-paid order that never created
			// one, or one predating the escrow column). There is no captured
			// escrow to protect, so cancellation is safe.
		}
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := tx.ReleaseReservation(ctx, orderID); err != nil {
		return err
	}
	// Status change goes in the SAME transaction as the restock. Previously it
	// was a separate unguarded statement after the commit, so a crash in
	// between left the order live with its stock already back on sale
	// (oversell), and a second sweeper could release the same reservation
	// twice (inflating stock).
	if err := tx.SetStatusGuarded(ctx, orderID, o.Status, domain.OrderCancelled); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: o.Status, ToStatus: domain.OrderCancelled, ActorID: actorID, Note: note,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publishOrderEvent(ctx, orderID, o.Status, domain.OrderCancelled)
	return nil
}

func (s *OrderService) publishOrderEvent(ctx context.Context, orderID, from, to string) {
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return
	}
	if s.broker != nil {
		ev := stream.Event{
			Type: "order.status_changed", OrderID: orderID, OrderNumber: o.OrderNumber,
			FromStatus: from, ToStatus: to, At: time.Now().UTC(),
		}
		_ = s.broker.Publish(ctx, o.BuyerID, ev)
		if o.SellerID != o.BuyerID {
			_ = s.broker.Publish(ctx, o.SellerID, ev)
		}
	}
	if s.notifications != nil {
		title := "Status pesanan berubah"
		body := "Pesanan " + o.OrderNumber + " kini: " + to
		_ = s.notifications.Notify(ctx, o.BuyerID, "order", title, body, map[string]any{"order_id": orderID, "status": to})
		if o.SellerID != o.BuyerID {
			_ = s.notifications.Notify(ctx, o.SellerID, "order", title, body, map[string]any{"order_id": orderID, "status": to})
		}
	}
}

// CancelExpired cancels pending orders past the payment deadline, releasing stock.
//
// Per-order failures are counted and logged rather than dropped. The previous
// version discarded every error and returned (0, nil), so asynq recorded the
// task as SUCCESS and a sweeper failing 100% of the time was indistinguishable
// from a healthy idle one — the exact failure this job exists to prevent going
// unnoticed.
func (s *OrderService) CancelExpired(ctx context.Context, limit int) (int, error) {
	ids, err := s.orders.ExpiredPendingOrders(ctx, time.Now().UTC().Add(-ReservationHold), limit)
	if err != nil {
		return 0, err
	}
	cancelled, failed := 0, 0
	for _, id := range ids {
		if err := s.CancelOrder(ctx, id, nil, "payment timeout"); err != nil {
			failed++
			if s.logger != nil {
				s.logger.Warn("failed to cancel expired order",
					"order_id", id, "error", err.Error())
			}
			continue
		}
		cancelled++
	}
	if failed > 0 {
		// Return an error so the task is retried and shows up in the queue
		// metrics. The successful cancellations are already committed
		// individually, so a retry is safe: the guarded status transition
		// makes a second attempt a no-op.
		return cancelled, fmt.Errorf("cancelled %d of %d expired orders; %d failed", cancelled, len(ids), failed)
	}
	return cancelled, nil
}

// AdvanceShippedOrders marks shipped orders delivered after the carrier
// window elapses (ghost-buyer sweep). Without this, orders strand in
// 'shipped' forever and escrow is never released to the seller. COD intents
// are captured at this point too.
func (s *OrderService) AdvanceShippedOrders(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	ids, err := s.orders.ShippedBefore(ctx, time.Now().UTC().Add(-olderThan), limit)
	if err != nil {
		return 0, err
	}
	advanced := 0
	for _, id := range ids {
		if err := s.orders.TransitionOrder(ctx, id, domain.OrderShipped, domain.OrderDelivered, "system"); err != nil {
			continue
		}
		s.publishOrderEvent(ctx, id, domain.OrderShipped, domain.OrderDelivered)
		if s.payments != nil {
			_ = s.payments.CaptureCOD(ctx, id)
		}
		advanced++
	}
	return advanced, nil
}

// CompleteDelivered auto-completes delivered orders past the confirmation window.
func (s *OrderService) CompleteDelivered(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	ids, err := s.orders.DeliveredBefore(ctx, time.Now().UTC().Add(-olderThan), limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, id := range ids {
		order, err := s.orders.ByID(ctx, id)
		if err != nil || order.Status != domain.OrderDelivered {
			continue
		}
		if err := s.orders.TransitionOrder(ctx, id, domain.OrderDelivered, domain.OrderCompleted, ""); err != nil {
			continue
		}
		s.publishOrderEvent(ctx, id, domain.OrderDelivered, domain.OrderCompleted)
		s.awardPoints(ctx, id)
		if s.payments != nil {
			if err := s.payments.ReleaseEscrow(ctx, id); err != nil {
				continue
			}
		}
		completed++
	}
	return completed, nil
}

// Reorder re-adds a past order's items to the buyer's cart (Beli Lagi).
func (s *OrderService) Reorder(ctx context.Context, userID, orderID string) (int, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return 0, err
	}
	if order.BuyerID != userID {
		return 0, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	cart, err := s.carts.EnsureActiveByUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, it := range order.Items {
		item := it
		if err := s.carts.AddItem(ctx, cart.ID, item.VariantID, item.Quantity); err != nil {
			continue
		}
		added++
	}
	return added, nil
}

// ListByBuyer lists orders with events.
func (s *OrderService) ListByBuyer(ctx context.Context, userID string, page, pageSize int) ([]*domain.Order, int64, error) {
	return s.orders.ListByBuyer(ctx, userID, page, pageSize)
}

// ListBySeller lists seller orders.
func (s *OrderService) ListBySeller(ctx context.Context, sellerID, status string, page, pageSize int) ([]*domain.Order, int64, error) {
	return s.orders.ListBySeller(ctx, sellerID, status, page, pageSize)
}

// ByID fetches an order if it belongs to the user (buyer or seller).
// ByID loads an order the caller is entitled to see.
//
// The signature used to be `ByID(ctx, orderID, requesterID string, isSeller
// bool)`, and the `bool` was the bug: the handler computed it with
// `user.HasRole(RoleSeller)` and passed it inward, so authorization was decided
// in the transport layer from a role the service could not re-derive. Two
// consequences. A user who holds BOTH roles — which is the normal case for any
// active seller, since sellers also buy — was routed down the seller branch for
// their own purchase and denied, because `o.SellerID != requesterID`. And
// `paid -> cancelled` is a legal transition, so a seller reaching their own
// sub-order through the buyer-facing cancel endpoint was authorised to cancel
// it, with only the escrow guard standing between them and a refund.
//
// The caller is now a domain.Actor, so every decision below is re-derivable
// from data the service received rather than data it was told to believe.
func (s *OrderService) ByID(ctx context.Context, orderID string, actor domain.Actor) (*domain.Order, error) {
	if actor.IsZero() {
		return nil, domain.ErrNotFound
	}
	o, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !actor.CanReadOrder(o.BuyerID, o.SellerID) {
		return nil, domain.ErrNotFound
	}
	return o, nil
}

// ByNumber fetches an order by its human-facing number, scoped to the caller.
//
// This backs `GET /orders/tracking/{number}`. It was previously unscoped: the
// query was a bare `WHERE o.order_number = $1` and the order number came from
// `nextval('order_number_seq')` (migration 00004, START 1000). Any account that
// could register for free could therefore walk the counter and read every order
// on the platform, including each buyer's UUID, the full money breakdown, the
// payment status, the coupon code, the seller name, and the full event timeline
// including the escrow-release and refund notes.
//
// A non-match is reported as NOT_FOUND rather than FORBIDDEN: "that order
// exists but is not yours" is a validity oracle, which is precisely what makes
// enumeration worth automating.
func (s *OrderService) ByNumber(ctx context.Context, orderNumber string, actor domain.Actor) (*domain.Order, error) {
	if actor.IsZero() {
		return nil, domain.ErrNotFound
	}
	return s.orders.ByNumberForViewer(ctx, orderNumber, actor)
}

// Events returns the timeline for an order.
func (s *OrderService) Events(ctx context.Context, orderID string) ([]*domain.OrderEvent, error) {
	return s.orders.Events(ctx, orderID)
}

// --- helpers ---

func (s *OrderService) validCoupon(ctx context.Context, userID, code string, subtotal float64) (*domain.Coupon, bool, error) {
	coupon, err := s.orders.CouponByCode(ctx, code)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	if !coupon.IsActive {
		return nil, false, domain.E(domain.KindConflict, "COUPON_INACTIVE", "coupon is inactive")
	}
	if now.Before(coupon.ValidFrom) || (coupon.ValidUntil != nil && now.After(*coupon.ValidUntil)) {
		return nil, false, domain.E(domain.KindConflict, "COUPON_EXPIRED", "coupon is expired")
	}
	if subtotal < coupon.MinSubtotal {
		return nil, false, domain.E(domain.KindConflict, "COUPON_MIN_NOT_MET",
			fmt.Sprintf("minimum subtotal of %.0f required", coupon.MinSubtotal))
	}
	if coupon.UsageLimit > 0 && coupon.UsedCount >= coupon.UsageLimit {
		return nil, false, domain.E(domain.KindConflict, "COUPON_EXHAUSTED", "coupon usage limit reached")
	}
	if coupon.PerUserLimit > 0 {
		used, err := s.orders.UsedCountForUser(ctx, coupon.ID, userID)
		if err != nil {
			return nil, false, err
		}
		if used >= coupon.PerUserLimit {
			return nil, false, domain.E(domain.KindConflict, "COUPON_USED", "coupon already used")
		}
	}
	return coupon, true, nil
}

func couponDiscount(c *domain.Coupon, subtotal float64) float64 {
	var d float64
	if c.Type == "percent" {
		d = subtotal * c.Value / 100
	} else {
		d = c.Value
	}
	if c.MaxDiscount != nil && d > *c.MaxDiscount {
		d = *c.MaxDiscount
	}
	if d > subtotal {
		d = subtotal
	}
	return d
}

func addressMap(a *domain.Address) map[string]any {
	return map[string]any{
		"recipient":     a.Recipient,
		"phone":         a.Phone,
		"address_line1": a.AddressLine1,
		"address_line2": a.AddressLine2,
		"city":          a.City,
		"province":      a.Province,
		"postal_code":   a.PostalCode,
		"country":       a.Country,
	}
}

// orderAddress picks a per-seller address when provided (multi-address checkout).
func orderAddress(fallback *domain.Address, bySeller map[string]*domain.Address, sellerID string) *domain.Address {
	if bySeller != nil {
		if a, ok := bySeller[sellerID]; ok && a != nil {
			return a
		}
	}
	return fallback
}
