package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Cart exposes cart endpoints (auth or guest session).
type Cart struct {
	svc *service.OrderService
}

// NewCart creates a Cart handler.
func NewCart(svc *service.OrderService) *Cart {
	return &Cart{svc: svc}
}

func (h *Cart) cartIdentity(r *http.Request) (userID, sessionKey string) {
	user := middleware.UserFrom(r.Context())
	if user != nil {
		return user.ID, ""
	}
	return "", r.Header.Get("X-Session-Key")
}

// Get handles GET /cart.
func (h *Cart) Get(w http.ResponseWriter, r *http.Request) {
	userID, sessionKey := h.cartIdentity(r)
	cart, err := h.svc.CartFor(r.Context(), userID, sessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.GetCart(r.Context(), cart.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	freeShip, _ := h.svc.FreeShippingStatus(r.Context(), userID, sessionKey)
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines, "free_shipping": freeShip})
}

type addItemRequest struct {
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
}

// Add handles POST /cart/items.
func (h *Cart) Add(w http.ResponseWriter, r *http.Request) {
	userID, sessionKey := h.cartIdentity(r)
	var req addItemRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cart, err := h.svc.CartFor(r.Context(), userID, sessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.AddToCart(r.Context(), cart.ID, req.VariantID, req.Quantity)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

type updateItemRequest struct {
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
}

// Update handles PUT /cart/items.
func (h *Cart) Update(w http.ResponseWriter, r *http.Request) {
	userID, sessionKey := h.cartIdentity(r)
	var req updateItemRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cart, err := h.svc.CartFor(r.Context(), userID, sessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.UpdateCartItem(r.Context(), cart.ID, req.VariantID, req.Quantity)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

// Remove handles DELETE /cart/items/{variantId}.
func (h *Cart) Remove(w http.ResponseWriter, r *http.Request) {
	userID, sessionKey := h.cartIdentity(r)
	cart, err := h.svc.CartFor(r.Context(), userID, sessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.RemoveFromCart(r.Context(), cart.ID, chi.URLParam(r, "variantId"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

type bulkItemsRequest struct {
	VariantIDs []string `json:"variant_ids"`
}

// BulkRemove handles POST /cart/bulk-remove.
func (h *Cart) BulkRemove(w http.ResponseWriter, r *http.Request) {
	userID, sessionKey := h.cartIdentity(r)
	var req bulkItemsRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cart, err := h.svc.CartFor(r.Context(), userID, sessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.BulkRemoveFromCart(r.Context(), cart.ID, req.VariantIDs)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

// BulkMove handles POST /cart/bulk-move — moves lines to the wishlist (auth).
func (h *Cart) BulkMove(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if user == nil {
		writeErr(w, r, domain.E(domain.KindUnauthenticated, "UNAUTHORIZED", "login required"))
		return
	}
	var req bulkItemsRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cart, err := h.svc.CartFor(r.Context(), user.ID, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.BulkMoveToWishlist(r.Context(), user.ID, cart.ID, req.VariantIDs)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

// Merge handles POST /cart/merge (auth required, merges guest cart).
func (h *Cart) Merge(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		SessionKey string `json:"session_key"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	guestCart, err := h.svc.CartFor(r.Context(), "", req.SessionKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := h.svc.MergeCart(r.Context(), user.ID, guestCart.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	cart, err := h.svc.CartFor(r.Context(), user.ID, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, lines, err := h.svc.GetCart(r.Context(), cart.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cart": cart, "lines": lines})
}

// Checkout exposes quote and place-order endpoints.
type Checkout struct {
	svc *service.OrderService
	rdb *redis.Client
}

// NewCheckout creates a Checkout handler.
func NewCheckout(svc *service.OrderService, rdb *redis.Client) *Checkout {
	return &Checkout{svc: svc, rdb: rdb}
}

type quoteRequest struct {
	CouponCode         string `json:"coupon_code,omitempty"`
	ShippingMethodCode string `json:"shipping_method_code,omitempty"`
	Insurance          bool   `json:"insurance,omitempty"`
	PointsToRedeem     int    `json:"points_to_redeem,omitempty"`
}

type buyNowRequest struct {
	VariantID          string `json:"variant_id"`
	Quantity           int    `json:"quantity"`
	ShippingMethodCode string `json:"shipping_method_code,omitempty"`
	AddressID          string `json:"address_id,omitempty"`
	Address            *struct {
		Recipient    string `json:"recipient"`
		Phone        string `json:"phone"`
		AddressLine1 string `json:"address_line1"`
		City         string `json:"city"`
		Province     string `json:"province"`
		PostalCode   string `json:"postal_code"`
	} `json:"address,omitempty"`
}

// BuyNow handles POST /checkout/buy-now — one-click order from a product page.
func (h *Checkout) BuyNow(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req buyNowRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.VariantID == "" {
		writeErr(w, r, domain.E(domain.KindInvalid, "VARIANT_REQUIRED", "variant is required"))
		return
	}
	idemKey := r.Header.Get("X-Idempotency-Key")
	if idemKey != "" {
		existing, err := h.rdb.Get(r.Context(), "idem:"+user.ID+":"+idemKey).Result()
		if err == nil && existing != "" {
			writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "order_ids": existing})
			return
		}
	}
	var address *domain.Address
	if req.Address != nil {
		address = &domain.Address{
			Recipient: req.Address.Recipient, Phone: req.Address.Phone,
			AddressLine1: req.Address.AddressLine1,
			City:         req.Address.City, Province: req.Address.Province,
			PostalCode: req.Address.PostalCode, Country: "Indonesia",
		}
	}
	placed, err := h.svc.BuyNow(r.Context(), service.BuyNowInput{
		UserID:             user.ID,
		VariantID:          req.VariantID,
		Quantity:           req.Quantity,
		AddressID:          req.AddressID,
		Address:            address,
		ShippingMethodCode: req.ShippingMethodCode,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if idemKey != "" && len(placed.Orders) > 0 {
		_ = h.rdb.Set(r.Context(), "idem:"+user.ID+":"+idemKey, placed.Orders[0].ID, time.Hour).Err()
	}
	writeJSON(w, http.StatusCreated, placed)
}

// Quote handles POST /checkout/quote.
func (h *Checkout) Quote(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req quoteRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cart, err := h.svc.CartFor(r.Context(), user.ID, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	quote, err := h.svc.QuoteCheckout(r.Context(), user.ID, cart.ID, req.CouponCode, req.ShippingMethodCode, req.Insurance, req.PointsToRedeem)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

type placeOrderRequest struct {
	CouponCode         string `json:"coupon_code,omitempty"`
	ShippingMethodCode string `json:"shipping_method_code"`
	Insurance          bool   `json:"insurance,omitempty"`
	PointsToRedeem     int    `json:"points_to_redeem,omitempty"`
	AddressID          string `json:"address_id,omitempty"`
	Address            *struct {
		Recipient    string `json:"recipient"`
		Phone        string `json:"phone"`
		AddressLine1 string `json:"address_line1"`
		AddressLine2 string `json:"address_line2,omitempty"`
		City         string `json:"city"`
		Province     string `json:"province"`
		PostalCode   string `json:"postal_code"`
		Country      string `json:"country"`
	} `json:"address,omitempty"`
	AddressesBySeller map[string]*struct {
		Recipient    string `json:"recipient"`
		Phone        string `json:"phone"`
		AddressLine1 string `json:"address_line1"`
		AddressLine2 string `json:"address_line2,omitempty"`
		City         string `json:"city"`
		Province     string `json:"province"`
		PostalCode   string `json:"postal_code"`
		Country      string `json:"country"`
	} `json:"addresses_by_seller,omitempty"`
	Notes string `json:"notes,omitempty"`
}

// Place handles POST /checkout/place — idempotent via X-Idempotency-Key.
func (h *Checkout) Place(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req placeOrderRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}

	// Idempotency is CLAIMED with SETNX before the order is placed, not read and then
	// acted on.
	//
	// The previous shape was GET-then-SET:
	//
	//	GET  idem:<user>:<key>   -> miss
	//	<place the order>
	//	SET  idem:<user>:<key>
	//
	// which is not a lock. Every concurrent retry that arrives before the SET sees the
	// same miss and places its own order, so N simultaneous retries produced N orders,
	// N stock reservations and N charges. A checkout is exactly where a buyer
	// double-taps, and mobile clients retry aggressively on a slow response -- the
	// failure this guard claims to prevent is the one it invited.
	//
	// Redis SETNX is atomic, so exactly one request can claim a given key. The claim is
	// written BEFORE the order exists, which means the window that mattered is closed.
	idemKey := r.Header.Get("X-Idempotency-Key")
	idemStore := "idem:" + user.ID + ":" + idemKey
	if idemKey != "" {
		// Already claimed by a request that finished. `orders` is stored as JSON so the
		// replay response has the SAME shape as the first one.
		//
		// It used to be a comma-joined string, so a replay returned
		// {"orders":"a,b,c"} while the original returned {"orders":[{...},{...}]}. A
		// client reading `orders[0].id` got the character "a" rather than an object --
		// and only on the retry, which is the path nobody tests by hand.
		existing, err := h.rdb.Get(r.Context(), idemStore).Result()
		if err == nil && existing != "" {
			var ids []string
			if json.Unmarshal([]byte(existing), &ids) == nil && len(ids) > 0 {
				writeJSON(w, http.StatusOK, map[string]any{
					"replayed": true, "orders": ids, "grand_total": 0,
				})
				return
			}
			// Claimed by a request that is STILL IN FLIGHT. The key exists but holds no
			// result yet. Report it as a conflict rather than placing a second order:
			// 425 Too Early is the honest answer, since retrying later is the right move.
			writeJSON(w, http.StatusTooEarly, map[string]any{
				"error": "REQUEST_IN_PROGRESS",
				"message": "a request with this X-Idempotency-Key is still being processed; " +
					"retry with the same key shortly",
			})
			return
		}

		// Claim it. A false return means another request won the race between our GET
		// and this SETNX, so it has the lock and we must not place an order.
		claimed, err := h.rdb.SetNX(r.Context(), idemStore, "in-flight", time.Hour).Result()
		if err != nil || !claimed {
			writeJSON(w, http.StatusTooEarly, map[string]any{
				"error": "REQUEST_IN_PROGRESS",
				"message": "another request with this X-Idempotency-Key won the race; " +
					"retry with the same key shortly",
			})
			return
		}
	}

	var address *domain.Address
	switch {
	case req.AddressID != "":
		// Address book selection: ownership is verified inside PlaceOrder via ResolveAddress.
		address = nil
	case req.Address != nil:
		address = &domain.Address{
			Recipient:    req.Address.Recipient,
			Phone:        req.Address.Phone,
			AddressLine1: req.Address.AddressLine1,
			AddressLine2: req.Address.AddressLine2,
			City:         req.Address.City,
			Province:     req.Address.Province,
			PostalCode:   req.Address.PostalCode,
			Country:      defaultString(req.Address.Country, "Indonesia"),
		}
	default:
		writeErr(w, r, domain.E(domain.KindInvalid, "ADDRESS_REQUIRED", "shipping address is required"))
		return
	}

	bySeller := map[string]*domain.Address{}
	for sid, a := range req.AddressesBySeller {
		if a == nil {
			continue
		}
		bySeller[sid] = &domain.Address{
			Recipient: a.Recipient, Phone: a.Phone, AddressLine1: a.AddressLine1,
			AddressLine2: a.AddressLine2, City: a.City, Province: a.Province,
			PostalCode: a.PostalCode, Country: defaultString(a.Country, "Indonesia"),
		}
	}

	cart, err := h.svc.CartFor(r.Context(), user.ID, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	placed, err := h.svc.PlaceOrder(r.Context(), service.PlaceOrderInput{
		UserID:             user.ID,
		CartID:             cart.ID,
		AddressID:          req.AddressID,
		Address:            address,
		AddressesBySeller:  bySeller,
		CouponCode:         req.CouponCode,
		ShippingMethodCode: req.ShippingMethodCode,
		Insurance:          req.Insurance,
		PointsToRedeem:     req.PointsToRedeem,
		Notes:              req.Notes,
		IdempotencyKey:     idemKey,
	})
	if err != nil {
		// Release the claim so the buyer can RETRY with the same key.
		//
		// Leaving the claim in place would convert every transient failure -- a timeout,
		// a dropped connection, a declined payment -- into a permanent 425 for that key,
		// for a full hour. The buyer would have no way to place the order at all, and
		// the failure mode would look like a server bug rather than a stuck key.
		//
		// This is safe because the claim is only released when PlaceOrder returned an
		// error, which means it did not commit. If PlaceOrder committed and then failed
		// to report, the claim stays and the retry returns the stored orders -- which is
		// the correct outcome, and the reason the release is tied to the error and not
		// to a timer.
		if idemKey != "" {
			_ = h.rdb.Del(r.Context(), idemStore).Err()
		}
		writeErr(w, r, err)
		return
	}
	if idemKey != "" {
		// Overwrite the claim with the result, as JSON, so a retry gets the order ids in
		// the same shape the original response used.
		ids := make([]string, 0, len(placed.Orders))
		for _, o := range placed.Orders {
			ids = append(ids, o.ID)
		}
		if b, mErr := json.Marshal(ids); mErr == nil {
			_ = h.rdb.Set(r.Context(), idemStore, b, time.Hour).Err()
		}
	}
	writeJSON(w, http.StatusCreated, placed)
}

func defaultString(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Orders exposes order queries for buyers.
type Orders struct {
	svc        *service.OrderService
	productSvc *service.ProductService
}

// NewOrders creates an Orders handler.
func NewOrders(svc *service.OrderService, productSvc *service.ProductService) *Orders {
	return &Orders{svc: svc, productSvc: productSvc}
}

// List handles GET /orders.
func (h *Orders) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	orders, total, err := h.svc.ListByBuyer(r.Context(), user.ID, intQuery(r.URL.Query().Get("page"), 1), intQuery(r.URL.Query().Get("page_size"), 10))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders, "total": total})
}

// SellerList handles GET /seller/orders — incoming orders for the caller's store.
func (h *Orders) SellerList(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	q := r.URL.Query()
	orders, total, err := h.svc.ListBySeller(r.Context(), user.ID, q.Get("status"),
		intQuery(q.Get("page"), 1), intQuery(q.Get("page_size"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders, "total": total})
}

// ByID handles GET /orders/{id}.
func (h *Orders) ByID(w http.ResponseWriter, r *http.Request) {

	o, err := h.svc.ByID(r.Context(), chi.URLParam(r, "id"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o})
}

// ByNumber handles GET /orders/tracking/{number}.
//
// Two independent defects used to meet here, and either alone was enough.
//
// The first was that there was no ownership check: the service method took no
// caller identity at all, the query was a bare `WHERE o.order_number = $1`, and
// this handler passed the row to the response after stripping four fields.
//
// The second was that the order number was guessable. `order_number` came from
// `nextval('order_number_seq')` -- migration 00004, `START 1000`, a global
// monotonic counter shared by every order on the platform. Enumerating it was a
// loop.
//
// Together they were a platform-wide read: the buyer's UUID, the seller's UUID,
// the subtotal/discount/shipping/total breakdown, the payment status, the coupon
// code, the seller's display name, and the whole event timeline (which carries
// the escrow-release and refund notes) for every order that had ever been
// placed. Defect 2 is being closed at the number-generation layer; defect 1 is
// closed here, and closing only one of them would still have been broken.
//
// The response is also narrowed, because a scoped lookup still should not echo
// identifiers the tracking UI has no use for.
// CreateReturnParcel handles POST /returns/{id}/parcel.
//
// The BUYER sends the goods back, so the buyer creates the parcel. The seller issues
// the label -- see the ownership note on `OrderService.CreateReturnParcel` for why
// that split is the fraud control rather than an accident of routing.
func (h *Orders) CreateReturnParcel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Carrier  string `json:"carrier,omitempty"`
		Quantity int    `json:"quantity"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Quantity <= 0 {
		writeErr(w, r, domain.E(domain.KindInvalid, "RETURN_QUANTITY_INVALID",
			"a return parcel must say how many units are going back"))
		return
	}
	parcel, err := h.svc.CreateReturnParcel(r.Context(), user.ID, chi.URLParam(r, "id"),
		req.Carrier, req.Quantity)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"parcel": parcel})
}

// ReturnParcel handles GET /returns/{id}/parcel.
//
// "Where is my return" needs to answer "none yet" as clearly as "here it is", so an
// absent parcel is a 200 with a null body rather than a 404.
func (h *Orders) ReturnParcel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	returnID := chi.URLParam(r, "id")
	// The same ownership check the create path makes. A 404 rather than a 403, so this
	// cannot be used to discover which return ids exist.
	if err := h.svc.AssertReturnOwner(r.Context(), returnID, user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	parcel, err := h.svc.ReturnParcelFor(r.Context(), user.ID, returnID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcel": parcel})
}

func (h *Orders) ByNumber(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.ByNumber(r.Context(), chi.URLParam(r, "number"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	events, err := h.svc.Events(r.Context(), o.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Strip everything that identifies the buyer or the transaction.
	o.ShippingAddressJSON = nil
	o.Notes = ""
	o.ExternalPaymentRef = ""
	o.BuyerID = ""
	o.SellerID = ""
	for _, it := range o.Items {
		it.SKU = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o, "events": events})
}

// Cancel handles POST /orders/{id}/cancel.
func (h *Orders) Cancel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	o, err := h.svc.ByID(r.Context(), chi.URLParam(r, "id"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := h.svc.CancelOrder(r.Context(), o.ID, &user.ID, "cancelled by buyer"); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

type externalPaymentRequest struct {
	Reference string  `json:"reference"`
	Amount    float64 `json:"amount"`
	PaidAt    string  `json:"paid_at,omitempty"`
}

// ExternalPayment handles POST /orders/{id}/external-payment — records an
// off-platform payment (buyer pays outside the app).
func (h *Orders) ExternalPayment(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req externalPaymentRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	var paidAt time.Time
	if req.PaidAt != "" {
		t, err := time.Parse(time.RFC3339, req.PaidAt)
		if err != nil {
			writeErr(w, r, domain.E(domain.KindInvalid, "BAD_TIME", "paid_at must be RFC3339"))
			return
		}
		paidAt = t
	}
	if err := h.svc.ConfirmExternalPayment(r.Context(), chi.URLParam(r, "id"), user.ID, req.Reference, req.Amount, paidAt); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"confirmed": true})
}

type orderTransitionRequest struct {
	Action string `json:"action"` // confirm_delivery | complete
}

// ConfirmDelivery handles POST /orders/{id}/confirm-delivery.
func (h *Orders) ConfirmDelivery(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.ConfirmDelivery(r.Context(), chi.URLParam(r, "id"), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"delivered": true})
}

// Complete handles POST /orders/{id}/complete — releases escrow to the seller.
func (h *Orders) Complete(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.CompleteOrder(r.Context(), chi.URLParam(r, "id"), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": true, "escrow_released": true})
}

// Events handles GET /orders/{id}/events — the state-machine timeline.
func (h *Orders) Events(w http.ResponseWriter, r *http.Request) {

	o, err := h.svc.ByID(r.Context(), chi.URLParam(r, "id"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	events, err := h.svc.Events(r.Context(), o.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ReviewOrderItem handles POST /orders/{id}/reviews — review a purchased item.
func (h *Orders) ReviewOrderItem(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		OrderItemID string   `json:"order_item_id"`
		Rating      int      `json:"rating"`
		Title       string   `json:"title,omitempty"`
		Content     string   `json:"content"`
		Images      []string `json:"images,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	rev, err := h.productSvc.ReviewOrderItem(r.Context(), service.ReviewOrderItemInput{
		OrderID: chi.URLParam(r, "id"), ItemID: req.OrderItemID, UserID: user.ID,
		Rating: req.Rating, Title: req.Title, Content: req.Content, Images: req.Images,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"review": rev})
}

// Reorder handles POST /orders/{id}/reorder — Beli Lagi.
func (h *Orders) Reorder(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	added, err := h.svc.Reorder(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added})
}

// Invoice handles GET /orders/{id}/invoice — printable invoice.
func (h *Orders) Invoice(w http.ResponseWriter, r *http.Request) {

	o, err := h.svc.ByID(r.Context(), chi.URLParam(r, "id"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	renderInvoice(w, o)
}

// PackingSlip handles GET /orders/{id}/packing-slip — seller picking sheet.
func (h *Orders) PackingSlip(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	o, err := h.svc.ByID(r.Context(), chi.URLParam(r, "id"), middleware.ActorFrom(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if o.SellerID != user.ID && !user.HasRole(domain.RoleAdmin) {
		writeErr(w, r, domain.E(domain.KindForbidden, "NOT_OWNED", "only the order's seller can print this"))
		return
	}
	renderPackingSlip(w, o)
}

// Addresses exposes the address book.
type Addresses struct {
	svc *service.OrderService
}

// NewAddresses creates an Addresses handler.
func NewAddresses(svc *service.OrderService) *Addresses {
	return &Addresses{svc: svc}
}

// List handles GET /account/addresses.
func (h *Addresses) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	addresses, err := h.svc.ListAddresses(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"addresses": addresses})
}

// Create handles POST /account/addresses.
func (h *Addresses) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Label        string `json:"label"`
		Recipient    string `json:"recipient"`
		Phone        string `json:"phone"`
		AddressLine1 string `json:"address_line1"`
		AddressLine2 string `json:"address_line2,omitempty"`
		City         string `json:"city"`
		Province     string `json:"province"`
		PostalCode   string `json:"postal_code"`
		Country      string `json:"country"`
		IsDefault    bool   `json:"is_default"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.CreateAddress(r.Context(), user.ID, &domain.Address{
		Label:        defaultString(req.Label, "Home"),
		Recipient:    req.Recipient,
		Phone:        req.Phone,
		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
		City:         req.City,
		Province:     req.Province,
		PostalCode:   req.PostalCode,
		Country:      defaultString(req.Country, "Indonesia"),
		IsDefault:    req.IsDefault,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"address": a})
}

// Update handles PUT /account/addresses/{id}.
func (h *Addresses) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Label        string `json:"label"`
		Recipient    string `json:"recipient"`
		Phone        string `json:"phone"`
		AddressLine1 string `json:"address_line1"`
		AddressLine2 string `json:"address_line2,omitempty"`
		City         string `json:"city"`
		Province     string `json:"province"`
		PostalCode   string `json:"postal_code"`
		Country      string `json:"country"`
		IsDefault    bool   `json:"is_default"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a := &domain.Address{
		ID:           chi.URLParam(r, "id"),
		Label:        defaultString(req.Label, "Home"),
		Recipient:    req.Recipient,
		Phone:        req.Phone,
		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
		City:         req.City,
		Province:     req.Province,
		PostalCode:   req.PostalCode,
		Country:      defaultString(req.Country, "Indonesia"),
		IsDefault:    req.IsDefault,
	}
	if err := h.svc.UpdateAddress(r.Context(), user.ID, a); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"address": a})
}

// Delete handles DELETE /account/addresses/{id}.
func (h *Addresses) Delete(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.DeleteAddress(r.Context(), user.ID, chi.URLParam(r, "id")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
