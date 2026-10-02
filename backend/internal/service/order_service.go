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
	"github.com/jackc/pgx/v5"
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
	// shipments is the parcel service. The BUYER creates a return parcel through
	// it, while the seller issues the label through SellerService -- see the
	// ownership note on `CreateReturnParcel` for why that split is deliberate.
	shipments    *ShipmentService
	mailer       *mail.Client
	webURL       string
	insurancePct float64
	logger       *slog.Logger
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
func (s *OrderService) SetShipmentService(ship *ShipmentService)      { s.shipments = ship }

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

	// subtotalBySeller and gramsBySeller carry the per-bundle figures the quote
	// computed, so PlaceOrder can build the sub-orders from the SAME numbers
	// rather than re-deriving them from the cart.
	//
	// It used to recompute both, in a second loop over the same cart lines:
	//
	//	subtotal += l.Subtotal
	//	weightKg += (l.WeightGrams * l.Quantity) / 1000
	//
	// which is a time-of-check/time-of-use split -- the quote is shown to the
	// buyer, then a second pass over a re-read cart produces the amount that is
	// actually persisted and charged. Any divergence between the two loops is a
	// buyer charged a different number than they were quoted, and the two loops
	// had already diverged on rounding. It is also why the weight bug was a
	// money bug rather than a display bug: this is the loop whose result becomes
	// `orders.total_amount`.
	subtotalBySeller map[string]float64
	gramsBySeller    map[string][]int
}

// DiscountFor returns the discount allocated to one seller bundle.
func (q *Quote) DiscountFor(sid string) float64 { return q.discountBySeller[sid] }

// SubtotalFor returns the merchandise subtotal the quote computed for one
// seller bundle. PlaceOrder must use this rather than re-summing the cart.
func (q *Quote) SubtotalFor(sid string) float64 { return q.subtotalBySeller[sid] }

// BillableKgFor returns the billable weight the quote computed for one bundle.
func (q *Quote) BillableKgFor(sid string) int { return billableKg(q.gramsBySeller[sid]) }

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
	//
	// This is allocateGlobal from money.go, extracted so it can be property
	// tested. The inline version it replaced rounded each non-final share to the
	// nearest rupiah, accumulated, and gave the last bundle the remainder; with
	// enough sellers the accumulated up-rounding exceeded the amount and the
	// last share went negative, which inflated that order's total AND tripped
	// migration 00040's `CHECK (discount_amount >= 0 AND discount_amount <=
	// subtotal)`, failing the whole multi-seller checkout with a 500.
	bundleSubtotals := map[string]float64{}
	bundleGrams := map[string][]int{}
	for _, sid := range sellerIDs {
		s2 := 0.0
		for _, l := range sellerBundles[sid] {
			s2 += moneyRound(l.Subtotal)
			bundleGrams[sid] = append(bundleGrams[sid], l.WeightGrams*l.Quantity)
		}
		bundleSubtotals[sid] = s2
	}
	quote.subtotalBySeller = bundleSubtotals
	quote.gramsBySeller = bundleGrams
	applyGlobalDiscount := func(amount float64) {
		for sid, share := range allocateGlobal(amount, sellerIDs, bundleSubtotals) {
			quote.discountBySeller[sid] += share
		}
	}
	if coupon != nil && coupon.SellerID == nil {
		applyGlobalDiscount(quote.DiscountAmount)
	}
	applyGlobalDiscount(quote.PointsDiscount)

	quote.InsuranceAvailable = s.insurancePct > 0
	quote.InsuranceSelected = insurance && quote.InsuranceAvailable

	for _, sid := range sellerIDs {
		bundleSubtotal := 0.0
		// Accumulate grams and convert once, at the end. The previous
		// `weightKg += (l.WeightGrams * l.Quantity) / 1000` divided per line
		// and summed, which truncated instead of rounding up AND made
		// truncate-per-line differ from truncate-of-sum: three 600g items plus
		// one 400g item is 2.2kg and was billed as 1kg, and six 500g items is
		// 3.0kg and was billed as 0kg -- free shipping on every order from a
		// seller whose catalogue is entirely sub-kilogram.
		weightKg := billableKg(bundleGrams[sid])
		bundleSubtotal = bundleSubtotals[sid]
		// store coupon discount scoped to this seller
		if coupon != nil && coupon.SellerID != nil && *coupon.SellerID == sid {
			d := couponDiscount(coupon, bundleSubtotal)
			quote.DiscountAmount = moneyRound(quote.DiscountAmount + d)
			total = moneyRound(total - d)
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
			fee := shippingFee(method.BaseFee, method.PerKgFee, weightKg)
			quote.Shipping = append(quote.Shipping, &ShippingQuote{
				MethodID: method.ID, Code: method.Code, Name: method.Name,
				BaseFee: method.BaseFee, PerKgFee: method.PerKgFee,
				WeightKg: weightKg, Fee: fee, MinDays: method.MinDays, MaxDays: method.MaxDays,
			})
			total = moneyRound(total + fee)
		}
		if quote.InsuranceSelected {
			sf := insuranceFee(bundleSubtotal, s.insurancePct)
			quote.Shipping[len(quote.Shipping)-1].InsuranceFee = sf
			quote.InsuranceFee = moneyRound(quote.InsuranceFee + sf)
			total = moneyRound(total + sf)
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

		// From the quote, not from a second pass over the cart. This used to be
		// `subtotal += l.Subtotal; weightKg += (l.WeightGrams * l.Quantity) / 1000`
		// in a loop of its own -- a time-of-check/time-of-use split where the
		// numbers shown to the buyer came from QuoteCheckout and the numbers
		// persisted and charged came from this second read. The two loops had
		// already diverged on rounding, and the weight loop was the one that
		// truncated per line, so a sub-kilogram bundle was persisted at 0kg.
		//
		// The billable weight is not needed here: shipping was already priced by
		// the quote, and `bundle` below is only used to snapshot the line items.
		subtotal := quote.SubtotalFor(sid)

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
			TotalAmount:     RoundIDR(subtotal - discount + shippingFee + insuranceFee),
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

		case ierr != nil && !domain.Is(ierr, domain.KindNotFound, ""):
			// Not "no such intent" — a real failure. Refuse: the cost of a
			// spurious refusal is one retry, the cost of a false "safe" is the
			// buyer's money.
			//
			// This was `errors.Is(ierr, domain.ErrNotFound)`, which is
			// ALWAYS false and so sent every failure down this arm. Two
			// consequences: the `default` arm below was unreachable, so an
			// order with payment_status='paid' and no payment_intents row
			// could never be cancelled at all; and an externally-paid order
			// creates no intent, so the buyer got a permanent 500 on cancel and
			// the seller's stock stayed reserved against an order nobody could
			// release.
			//
			// domain.Is matches on Kind alone, because IntentByOrder reports
			// absence as `E(KindNotFound, "NO_INTENT", ...)`, a different Code
			// from the ErrNotFound sentinel's "NOT_FOUND". Matching on the
			// sentinel would need the codes unified; matching on the kind is
			// the honest question, which is "is this a not-found, or a fault?".
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
			s.log().Warn("failed to cancel expired order",
				"order_id", id, "error", err.Error())
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
//
// This job was completely inert. It passed the actor literal "system", and
// OrderTx.AddEvent casts it with `NULLIF($4,”)::uuid`, so Postgres raised
// `22P02 invalid input syntax for type uuid` on every single order, the
// surrounding transaction rolled back, and the error was discarded by a bare
// `continue`. The job returned (0, nil), so asynq recorded every run as
// SUCCESS. Net effect: no order was ever auto-delivered, no escrow was ever
// auto-released, and no COD was ever captured for a ghost buyer -- while the
// queue dashboard showed a healthy green job. The sibling sweep
// `CompleteDelivered` passed "" (which NULLIF turns into NULL) and worked, which
// is what the intent always was: an empty actor means "system", so the
// attribution column is NULL rather than a fake user id.
//
// Failure handling now matches CancelExpired: count, log, and return an error so
// the task is retried and shows up in queue metrics.
func (s *OrderService) AdvanceShippedOrders(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	ids, err := s.orders.ShippedBefore(ctx, time.Now().UTC().Add(-olderThan), limit)
	if err != nil {
		return 0, err
	}
	advanced, failed := 0, 0
	for _, id := range ids {
		// Empty string, not "system": AddEvent does NULLIF($4,'')::uuid, so ""
		// becomes a NULL actor_id (a system action) whereas "system" is a
		// malformed uuid and aborts the transaction.
		if err := s.orders.TransitionOrder(ctx, id, domain.OrderShipped, domain.OrderDelivered, ""); err != nil {
			failed++
			s.log().Warn("failed to advance shipped order to delivered",
				"order_id", id, "error", err.Error())
			continue
		}
		s.publishOrderEvent(ctx, id, domain.OrderShipped, domain.OrderDelivered)
		if s.payments == nil {
			// Not a "skip quietly" case. Without the payment service the COD
			// capture below cannot run, which is the entire point of this sweep
			// for COD orders. Surface it rather than reporting success.
			failed++
			s.log().Error("cannot capture COD: payment service is not wired",
				"order_id", id, "hint", "cmd/worker builds services separately and must set the payment service")
			continue
		}
		if err := s.payments.CaptureCOD(ctx, id); err != nil {
			// The order is already delivered and the sweep's own predicate
			// (status='shipped') will never select it again, so a failure here
			// is permanent unless something retries it. Count it and let the
			// returned error drive a retry; the guarded transition makes a
			// second attempt a no-op for the status write and CaptureCOD is
			// itself guarded on intent status.
			failed++
			s.log().Error("failed to capture COD for ghost-buyer order",
				"order_id", id, "error", err.Error())
			continue
		}
		advanced++
	}
	if failed > 0 {
		return advanced, fmt.Errorf("advanced %d of %d shipped orders; %d failed", advanced, len(ids), failed)
	}
	return advanced, nil
}

// CompleteDelivered auto-completes delivered orders past the confirmation window.
//
// Ordering matters here and was wrong. This used to transition the order to
// `completed` first and release escrow last, and it swallowed the release error
// with a bare `continue`. The sweeper's own predicate is
// `status = 'delivered'`, so an order whose escrow release failed was already
// `completed`, was never selected again, and the seller was never paid -- with
// no log line, no counter, and asynq recording SUCCESS. That is permanent
// seller non-payment triggered by one transient database error.
//
// Escrow is therefore released BEFORE the status transition. If the release
// fails the order stays `delivered`, the next sweep run selects it again, and
// ReleaseEscrow is guarded on the intent's status so the retry is idempotent.
// Releasing on `delivered` is also the correct semantic: the money moves when the
// buyer has the goods, and the completion is a formality on top of that.
func (s *OrderService) CompleteDelivered(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	ids, err := s.orders.DeliveredBefore(ctx, time.Now().UTC().Add(-olderThan), limit)
	if err != nil {
		return 0, err
	}
	completed, failed := 0, 0
	for _, id := range ids {
		order, err := s.orders.ByID(ctx, id)
		if err != nil {
			failed++
			s.log().Warn("failed to load delivered order", "order_id", id, "error", err.Error())
			continue
		}
		if order.Status != domain.OrderDelivered {
			// Another actor (buyer confirm, admin) got there first. Not a
			// failure -- the guarded transition below would reject it anyway.
			continue
		}
		if s.payments == nil {
			failed++
			s.log().Error("cannot release escrow: payment service is not wired", "order_id", id)
			continue
		}
		// Pay the seller FIRST. If this fails, the order stays `delivered` and
		// the next run retries it. See the doc comment for why the order used
		// to become permanently uncompletable.
		if err := s.payments.ReleaseEscrow(ctx, id); err != nil {
			failed++
			s.log().Error("failed to release escrow; order left delivered for retry",
				"order_id", id, "error", err.Error())
			continue
		}
		if err := s.orders.TransitionOrder(ctx, id, domain.OrderDelivered, domain.OrderCompleted, ""); err != nil {
			failed++
			s.log().Warn("escrow released but status transition failed",
				"order_id", id, "error", err.Error())
			continue
		}
		s.publishOrderEvent(ctx, id, domain.OrderDelivered, domain.OrderCompleted)
		s.awardPoints(ctx, id)
		completed++
	}
	if failed > 0 {
		return completed, fmt.Errorf("completed %d of %d delivered orders; %d failed", completed, len(ids), failed)
	}
	return completed, nil
}

// log returns the service logger, never nil.
//
// A nil-logger guard is how the worker ended up silently discarding every
// per-order failure: cmd/worker built its services without calling SetLogger,
// so `if s.logger != nil` skipped the only line that would have shown the
// problem. A sweeper that cannot report a failure is a sweeper that fails
// silently, so this falls back to the default slog logger instead.
func (s *OrderService) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
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

// couponDiscount is the discount a coupon grants on a subtotal.
//
// Rounded to whole rupiah. It previously returned the raw float, so a 3% coupon
// on Rp12,345.67 produced 370.37010000000004 -- which was written to
// `orders.discount_amount` and returned to the frontend as the quoted discount,
// while the buyer was charged 370.37. The quote and the charge disagreed, and
// the JSON the client rendered was a float the currency does not have.
//
// The clamp to `subtotal` is load-bearing for a 100% coupon combined with any
// other discount source (loyalty points): without it the sum of the two
// allocations exceeds the subtotal and migration 00040's
// `CHECK (discount_amount <= subtotal)` rejects the order.
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
	if d < 0 {
		d = 0
	}
	return RoundIDR(d)
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

// CreateReturnParcel records the box a buyer's return travels in.
//
// THE BUYER CREATES IT; THE SELLER ISSUES THE LABEL. That split is the whole fraud
// control on this feature.
//
// A seller who could create and dispatch a return parcel could mark goods as returned
// without them ever having left the buyer's house, and the refund that follows is real
// money. So the party that physically hands the parcel over is the party that records
// it, and the party that pays for the carriage and owns the destination -- the seller
// -- is the one who buys the label.
//
// The identity check is against the RETURN's buyer, not the order's, because a return
// is per order line and the buyer on it is the authoritative party for this claim.
func (s *OrderService) CreateReturnParcel(
	ctx context.Context, buyerID, returnID, carrier string, quantity int,
) (ParcelView, error) {
	if s.shipments == nil {
		return ParcelView{}, domain.E(domain.KindInternal, "SHIPMENTS_NOT_WIRED",
			"the shipment service is not available, so a return cannot be sent back")
	}
	if err := s.assertReturnBuyer(ctx, returnID, buyerID); err != nil {
		return ParcelView{}, err
	}
	return s.shipments.CreateReturnParcel(ctx, returnID, carrier, quantity)
}

// ReturnParcelFor returns the parcel this buyer's return travels in.
func (s *OrderService) ReturnParcelFor(ctx context.Context, returnID string) (*repository.ReturnParcel, error) {
	if s.shipments == nil {
		return nil, domain.E(domain.KindInternal, "SHIPMENTS_NOT_WIRED",
			"the shipment service is not available, so return parcels cannot be read")
	}
	return s.shipments.ReturnParcelFor(ctx, returnID)
}

// AssertReturnOwner refuses a return that is not the caller's.
//
// Exported for the handler, which must NOT run its own ownership query: two checks in
// two packages drift, and the one that drifts is the read path, where nobody notices
// because in every test the data already belongs to the caller.
func (s *OrderService) AssertReturnOwner(ctx context.Context, returnID, buyerID string) error {
	return s.assertReturnBuyer(ctx, returnID, buyerID)
}

// assertReturnBuyer refuses a return claim that is not the caller's.
//
// A NOT_FOUND rather than a FORBIDDEN for someone else's return, so the endpoint
// cannot be used to discover which return ids exist -- the same distinction the order
// detail endpoint draws.
//
// The check is against the RETURN's buyer rather than the order's. A return is per
// order line, and the buyer recorded on the return is the authoritative party for that
// claim; using the order's buyer would be correct only while the two always agree,
// which is exactly the assumption that rots.
func (s *OrderService) assertReturnBuyer(ctx context.Context, returnID, buyerID string) error {
	if s.stores == nil {
		return domain.E(domain.KindInternal, "STORES_NOT_WIRED",
			"the store repository is not available, so the return cannot be checked")
	}
	_, _, owner, _, _, err := s.stores.ReturnFacts(ctx, s.stores.Pool(), returnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != buyerID {
		return domain.ErrNotFound
	}
	return nil
}
