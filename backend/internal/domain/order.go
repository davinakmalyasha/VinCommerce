package domain

import "time"

// Order statuses (state machine).
const (
	OrderPending         = "pending"
	OrderPaid            = "paid"
	OrderPacked          = "packed"
	OrderShipped         = "shipped"
	OrderDelivered       = "delivered"
	OrderCompleted       = "completed"
	OrderCancelled       = "cancelled"
	OrderReturnRequested = "return_requested"
	OrderReturned        = "returned"
)

// Payment statuses.
const (
	PaymentUnpaid            = "unpaid"
	PaymentPending           = "pending"
	PaymentPaid              = "paid"
	PaymentRefunded          = "refunded"
	PaymentPartiallyRefunded = "partially_refunded"
)

// Cart statuses.
const (
	CartActive     = "active"
	CartMerged     = "merged"
	CartCheckedOut = "checked_out"
)

// Order transitions allowed by the state machine.
var allowedTransitions = map[string][]string{
	OrderPending:         {OrderPaid, OrderCancelled},
	OrderPaid:            {OrderPacked, OrderCancelled},
	OrderPacked:          {OrderShipped, OrderCancelled},
	OrderShipped:         {OrderDelivered},
	OrderDelivered:       {OrderCompleted, OrderReturnRequested},
	OrderCompleted:       {OrderReturnRequested},
	OrderReturnRequested: {OrderReturned, OrderCancelled},
}

// CanTransition reports whether from->to is allowed.
func CanTransition(from, to string) bool {
	targets, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// Order is a per-seller sub-order created at checkout.
type Order struct {
	ID              string         `json:"id"`
	OrderNumber     string         `json:"order_number"`
	BuyerID         string         `json:"buyer_id"`
	SellerID        string         `json:"seller_id"`
	Status          string         `json:"status"`
	Currency        string         `json:"currency"`
	Subtotal        float64        `json:"subtotal"`
	DiscountAmount  float64        `json:"discount_amount"`
	ShippingFee     float64        `json:"shipping_fee"`
	TotalAmount     float64        `json:"total_amount"`
	PaymentStatus   string         `json:"payment_status"`
	CouponCode      string         `json:"coupon_code,omitempty"`
	ShippingAddress map[string]any `json:"shipping_address"`
	ShippingMethod  string         `json:"shipping_method,omitempty"`
	Notes           string         `json:"notes,omitempty"`
	PlacedAt        time.Time      `json:"placed_at"`
	PaidAt          *time.Time     `json:"paid_at,omitempty"`
	ShippedAt       *time.Time     `json:"shipped_at,omitempty"`
	DeliveredAt     *time.Time     `json:"delivered_at,omitempty"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
	CancelledAt     *time.Time     `json:"cancelled_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	Items  []*OrderItem  `json:"items,omitempty"`
	Seller *StoreSummary `json:"seller,omitempty"`

	ShippingAddressJSON []byte `json:"-"`
	BuyerName           string `json:"buyer_name,omitempty"`
	SellerName          string `json:"seller_name,omitempty"`

	TrackingNumber string `json:"tracking_number,omitempty"`
	Carrier        string `json:"carrier,omitempty"`

	ExternalPaymentRef string     `json:"external_payment_ref,omitempty"`
	ExternalPaidAt     *time.Time `json:"external_paid_at,omitempty"`
}

// OrderItem is a snapshot of a purchased variant.
type OrderItem struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`
	ProductID   string    `json:"product_id"`
	VariantID   string    `json:"variant_id"`
	SellerID    string    `json:"seller_id"`
	ProductName string    `json:"product_name"`
	VariantName string    `json:"variant_name"`
	SKU         string    `json:"sku"`
	UnitPrice   float64   `json:"unit_price"`
	Quantity    int       `json:"quantity"`
	WeightGrams int       `json:"weight_grams"`
	Total       float64   `json:"total"`
	ImageURL    string    `json:"image_url,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// OrderEvent is a state-machine transition record.
type OrderEvent struct {
	ID         int64     `json:"id"`
	OrderID    string    `json:"order_id"`
	FromStatus string    `json:"from_status,omitempty"`
	ToStatus   string    `json:"to_status"`
	ActorID    *string   `json:"actor_id,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Cart aggregates items for a user or guest session.
type Cart struct {
	ID         string      `json:"id"`
	UserID     *string     `json:"user_id,omitempty"`
	SessionKey string      `json:"session_key,omitempty"`
	Status     string      `json:"status"`
	Items      []*CartItem `json:"items"`
}

// CartItem links a variant with a quantity.
type CartItem struct {
	ID        string `json:"id"`
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
}

// CartLine is a cart item enriched with product data for display.
type CartLine struct {
	CartItem
	ProductID   string  `json:"product_id"`
	ProductSlug string  `json:"product_slug"`
	ProductName string  `json:"product_name"`
	VariantName string  `json:"variant_name"`
	SKU         string  `json:"sku"`
	ImageURL    string  `json:"image_url,omitempty"`
	Price       float64 `json:"price"`
	Subtotal    float64 `json:"subtotal"`
	Stock       int     `json:"stock"`
	SellerID    string  `json:"seller_id"`
	SellerName  string  `json:"seller_name"`
	WeightGrams int     `json:"weight_grams"`
	IsActive    bool    `json:"is_active"`
}

// Address is a shipping destination.
type Address struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Label        string    `json:"label"`
	Recipient    string    `json:"recipient"`
	Phone        string    `json:"phone"`
	AddressLine1 string    `json:"address_line1"`
	AddressLine2 string    `json:"address_line2,omitempty"`
	City         string    `json:"city"`
	Province     string    `json:"province"`
	PostalCode   string    `json:"postal_code"`
	Country      string    `json:"country"`
	IsDefault    bool      `json:"is_default"`
	CreatedAt    time.Time `json:"created_at"`
}

// Coupon is a discount code.
type Coupon struct {
	ID           string     `json:"id"`
	Code         string     `json:"code"`
	Type         string     `json:"type"` // percent | fixed
	Value        float64    `json:"value"`
	MinSubtotal  float64    `json:"min_subtotal"`
	MaxDiscount  *float64   `json:"max_discount,omitempty"`
	UsageLimit   int        `json:"usage_limit"`
	UsedCount    int        `json:"used_count"`
	PerUserLimit int        `json:"per_user_limit"`
	ValidFrom    time.Time  `json:"valid_from"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	IsActive     bool       `json:"is_active"`
	SellerID     *string    `json:"seller_id,omitempty"`
	StoreName    string     `json:"store_name,omitempty"`
}

// ShippingMethod describes a delivery option.
type ShippingMethod struct {
	ID       string  `json:"id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	BaseFee  float64 `json:"base_fee"`
	PerKgFee float64 `json:"per_kg_fee"`
	MinDays  int     `json:"min_days"`
	MaxDays  int     `json:"max_days"`
	IsActive bool    `json:"is_active"`
}
