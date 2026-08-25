package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// OrderRepository persists orders, items, events, coupons and stock.
type OrderRepository struct {
	pool *db.Pool
}

// NewOrderRepository creates an OrderRepository.
func NewOrderRepository(pool *db.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

// Place inserts orders and items within a transaction, applying inventory effects.
// Returns the created orders. The tx must be committed by the caller.
type OrderTx struct {
	tx pgx.Tx
}

// Begin starts the checkout transaction.
func (r *OrderRepository) Begin(ctx context.Context) (*OrderTx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &OrderTx{tx: tx}, nil
}

// Rollback aborts the transaction.
func (t *OrderTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// Commit finalizes the transaction.
func (t *OrderTx) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

// PgTx exposes the underlying transaction so other repositories (payments,
// wallets) can participate in the same atomic unit of work.
func (t *OrderTx) PgTx() pgx.Tx { return t.tx }

// SetStatus updates the order status inside this transaction.
func (t *OrderTx) SetStatus(ctx context.Context, orderID, status string) error {
	tag, err := t.tx.Exec(ctx, `
		UPDATE orders SET status = $2::varchar, updated_at = now(),
			paid_at = CASE WHEN $2::varchar = 'paid' THEN now() ELSE paid_at END,
			shipped_at = CASE WHEN $2::varchar = 'shipped' THEN now() ELSE shipped_at END,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN now() ELSE delivered_at END,
			completed_at = CASE WHEN $2::varchar = 'completed' THEN now() ELSE completed_at END,
			cancelled_at = CASE WHEN $2::varchar = 'cancelled' THEN now() ELSE cancelled_at END
		WHERE id = $1 AND status <> $2::varchar`, orderID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetStatusGuarded updates the order status ONLY when it currently equals
// `from` (optimistic state-machine guard for money-critical transitions).
func (t *OrderTx) SetStatusGuarded(ctx context.Context, orderID, from, to string) error {
	tag, err := t.tx.Exec(ctx, `
		UPDATE orders SET status = $3::varchar, updated_at = now(),
			paid_at = CASE WHEN $3::varchar = 'paid' THEN now() ELSE paid_at END,
			shipped_at = CASE WHEN $3::varchar = 'shipped' THEN now() ELSE shipped_at END,
			delivered_at = CASE WHEN $3::varchar = 'delivered' THEN now() ELSE delivered_at END,
			completed_at = CASE WHEN $3::varchar = 'completed' THEN now() ELSE completed_at END,
			cancelled_at = CASE WHEN $3::varchar = 'cancelled' THEN now() ELSE cancelled_at END
		WHERE id = $1 AND status = $2::varchar`, orderID, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION",
			"order is not in the expected state")
	}
	return nil
}

// SetPaymentStatus updates the payment status inside this transaction.
func (t *OrderTx) SetPaymentStatus(ctx context.Context, orderID, paymentStatus string) error {
	_, err := t.tx.Exec(ctx,
		`UPDATE orders SET payment_status = $2, updated_at = now() WHERE id = $1`, orderID, paymentStatus)
	return err
}

// ClearCart deletes all lines of a cart inside the checkout transaction.
func (t *OrderTx) ClearCart(ctx context.Context, cartID string) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cartID)
	return err
}

// NextOrderNumber fetches the next value of the order number sequence.
func (t *OrderTx) NextOrderNumber(ctx context.Context) (int64, error) {
	var n int64
	err := t.tx.QueryRow(ctx, `SELECT nextval('order_number_seq')`).Scan(&n)
	return n, err
}

// CreateOrder inserts an order and returns it.
func (t *OrderTx) CreateOrder(ctx context.Context, o *domain.Order) error {
	addr, err := json.Marshal(o.ShippingAddress)
	if err != nil {
		return err
	}
	err = t.tx.QueryRow(ctx, `
		INSERT INTO orders (id, order_number, buyer_id, seller_id, status, currency,
		                    subtotal, discount_amount, shipping_fee, insurance_fee, total_amount, payment_status,
		                    coupon_code, shipping_address, shipping_method, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14, NULLIF($15, ''), NULLIF($16, ''))
		RETURNING placed_at`,
		o.ID, o.OrderNumber, o.BuyerID, o.SellerID, o.Status, o.Currency,
		o.Subtotal, o.DiscountAmount, o.ShippingFee, o.InsuranceFee, o.TotalAmount, o.PaymentStatus,
		o.CouponCode, addr, o.ShippingMethod, o.Notes).Scan(&o.PlacedAt)
	return err
}

// CreateItem inserts an order item.
func (t *OrderTx) CreateItem(ctx context.Context, it *domain.OrderItem) error {
	return t.tx.QueryRow(ctx, `
		INSERT INTO order_items (id, order_id, product_id, variant_id, seller_id, product_name,
		                         variant_name, sku, unit_price, quantity, weight_grams, total, image_url, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14)
		RETURNING created_at`,
		it.ID, it.OrderID, it.ProductID, it.VariantID, it.SellerID, it.ProductName,
		it.VariantName, it.SKU, it.UnitPrice, it.Quantity, it.WeightGrams, it.Total, it.ImageURL, it.Status).
		Scan(&it.CreatedAt)
}

// ItemByID loads one order item (ownership checks happen at the service layer).
func (r *OrderRepository) ItemByID(ctx context.Context, itemID string) (*domain.OrderItem, error) {
	var it domain.OrderItem
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_id, product_id, variant_id FROM order_items WHERE id = $1`, itemID).
		Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// AddEvent records a state transition.
func (t *OrderTx) AddEvent(ctx context.Context, e *domain.OrderEvent) error {
	_, err := t.tx.Exec(ctx, `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id, note)
		VALUES ($1, NULLIF($2, ''), $3, $4, NULLIF($5, ''))`,
		e.OrderID, e.FromStatus, e.ToStatus, e.ActorID, e.Note)
	return err
}

// ReserveStock locks a variant row, decrements stock, writes a reservation and ledger entry.
func (t *OrderTx) ReserveStock(ctx context.Context, variantID, orderID string, quantity int, holdFor time.Duration) error {
	var stock int
	if err := t.tx.QueryRow(ctx,
		`SELECT stock FROM product_variants WHERE id = $1 FOR UPDATE`, variantID).Scan(&stock); err != nil {
		return err
	}
	if stock < quantity {
		return domain.E(domain.KindConflict, "INSUFFICIENT_STOCK", "insufficient stock for item in cart")
	}
	if _, err := t.tx.Exec(ctx,
		`UPDATE product_variants SET stock = stock - $2, updated_at = now() WHERE id = $1`,
		variantID, quantity); err != nil {
		return err
	}
	if _, err := t.tx.Exec(ctx, `
		INSERT INTO inventory_reservations (id, variant_id, order_id, quantity, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3, now() + $4::interval)`,
		variantID, orderID, quantity, holdFor.String()); err != nil {
		return err
	}
	_, err := t.tx.Exec(ctx, `
		INSERT INTO stock_ledger (variant_id, order_id, change, reason)
		VALUES ($1, $2, $3, 'reserve')`,
		variantID, orderID, -quantity)
	return err
}

// ConsumeReservation marks a reservation consumed (payment captured).
func (t *OrderTx) ConsumeReservation(ctx context.Context, orderID string) error {
	_, err := t.tx.Exec(ctx, `
		UPDATE inventory_reservations SET status = 'consumed'
		WHERE order_id = $1 AND status = 'held'`, orderID)
	return err
}

// MarkExternalPaid records an off-platform payment: flips the order to paid,
// stamps the external reference, consumes the stock reservation and logs the event.
func (t *OrderTx) MarkExternalPaid(ctx context.Context, orderID, ref string, paidAt time.Time) error {
	tag, err := t.tx.Exec(ctx, `
		UPDATE orders
		SET status = 'paid', payment_status = 'paid', paid_at = now(),
		    external_payment_ref = NULLIF($2, ''), external_paid_at = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, orderID, ref, paidAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "NOT_PENDING", "order is not awaiting payment")
	}
	if err := t.ConsumeReservation(ctx, orderID); err != nil {
		return err
	}
	return t.AddEvent(ctx, &domain.OrderEvent{
		OrderID: orderID, FromStatus: domain.OrderPending, ToStatus: domain.OrderPaid,
		Note: "paid externally, ref: " + ref,
	})
}

// ReleaseReservation restocks and marks reservations released (cancel/timeout).
func (t *OrderTx) ReleaseReservation(ctx context.Context, orderID string) error {
	_, err := t.tx.Exec(ctx, `
		UPDATE product_variants v
		SET stock = v.stock + r.quantity, updated_at = now()
		FROM inventory_reservations r
		WHERE r.variant_id = v.id AND r.order_id = $1 AND r.status = 'held'`, orderID)
	if err != nil {
		return err
	}
	tag, err := t.tx.Exec(ctx, `
		UPDATE inventory_reservations SET status = 'released'
		WHERE order_id = $1 AND status = 'held'`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		_, err = t.tx.Exec(ctx, `
			INSERT INTO stock_ledger (variant_id, order_id, change, reason)
			SELECT variant_id, order_id, quantity, 'release'
			FROM inventory_reservations WHERE order_id = $1 AND status = 'released'`, orderID)
	}
	return err
}

// IncrementCouponUsage claims a coupon use (locking the row).
// usage_limit = 0 means unlimited, so the guard only applies when a limit is set.
func (t *OrderTx) IncrementCouponUsage(ctx context.Context, couponID, userID, orderID string) error {
	tag, err := t.tx.Exec(ctx, `
		UPDATE coupons SET used_count = used_count + 1
		WHERE id = $1 AND (usage_limit = 0 OR used_count < usage_limit)`, couponID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "COUPON_EXHAUSTED", "coupon usage limit reached")
	}
	_, err = t.tx.Exec(ctx, `
		INSERT INTO coupon_usages (coupon_id, user_id, order_id) VALUES ($1, $2, $3)`,
		couponID, userID, orderID)
	return err
}

// ListByBuyer returns the buyer's orders.
func (r *OrderRepository) ListByBuyer(ctx context.Context, userID string, page, pageSize int) ([]*domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM orders WHERE buyer_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT o.id, o.order_number, o.buyer_id, o.seller_id, o.status, o.currency,
		       o.subtotal, o.discount_amount, o.shipping_fee, o.total_amount, o.payment_status,
		       COALESCE(o.coupon_code,''), o.shipping_address, o.shipping_method, COALESCE(o.notes,''), COALESCE(o.tracking_number,''), COALESCE(o.carrier,''),
		       o.placed_at, o.paid_at, o.shipped_at, o.delivered_at, o.completed_at, o.cancelled_at,
		       o.created_at, o.updated_at, u.full_name,
		       COALESCE(o.external_payment_ref,''), o.external_paid_at
		FROM orders o
		JOIN users u ON u.id = o.seller_id
		WHERE o.buyer_id = $1
		ORDER BY o.created_at DESC
		LIMIT $2 OFFSET $3`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	orders := []*domain.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		orders = append(orders, o)
	}
	return orders, total, rows.Err()
}

// ListBySeller returns orders for a seller's dashboard.
func (r *OrderRepository) ListBySeller(ctx context.Context, sellerID string, status string, page, pageSize int) ([]*domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	where := `o.seller_id = $1`
	args := []any{sellerID}
	if status != "" {
		args = append(args, status)
		where += ` AND o.status = $2`
	}
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM orders o WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.pool.Query(ctx, `
		SELECT o.id, o.order_number, o.buyer_id, o.seller_id, o.status, o.currency,
		       o.subtotal, o.discount_amount, o.shipping_fee, o.total_amount, o.payment_status,
		       COALESCE(o.coupon_code,''), o.shipping_address, o.shipping_method, COALESCE(o.notes,''), COALESCE(o.tracking_number,''), COALESCE(o.carrier,''),
		       o.placed_at, o.paid_at, o.shipped_at, o.delivered_at, o.completed_at, o.cancelled_at,
		       o.created_at, o.updated_at, b.full_name,
		       COALESCE(o.external_payment_ref,''), o.external_paid_at
		FROM orders o
		JOIN users b ON b.id = o.buyer_id
		WHERE `+where+`
		ORDER BY o.created_at DESC
		LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	orders := []*domain.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		orders = append(orders, o)
	}
	return orders, total, rows.Err()
}

// ByID loads an order with items and events.
func (r *OrderRepository) ByID(ctx context.Context, orderID string) (*domain.Order, error) {
	o, err := r.byOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return r.loadItems(ctx, o)
}

// OrderByNumber fetches an order by its human-readable number.
func (r *OrderRepository) OrderByNumber(ctx context.Context, orderNumber string) (*domain.Order, error) {
	o, err := r.byNumber(ctx, orderNumber)
	if err != nil {
		return nil, err
	}
	return r.loadItems(ctx, o)
}

func (r *OrderRepository) byOrderID(ctx context.Context, orderID string) (*domain.Order, error) {
	var o domain.Order
	var sellerName string
	err := r.pool.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.buyer_id, o.seller_id, o.status, o.currency,
		       o.subtotal, o.discount_amount, o.shipping_fee, o.total_amount, o.payment_status,
		       COALESCE(o.coupon_code,''), o.shipping_address, o.shipping_method, COALESCE(o.notes,''), COALESCE(o.tracking_number,''), COALESCE(o.carrier,''),
		       o.placed_at, o.paid_at, o.shipped_at, o.delivered_at, o.completed_at, o.cancelled_at,
		       o.created_at, o.updated_at, u.full_name,
		       COALESCE(o.external_payment_ref,''), o.external_paid_at
		FROM orders o
		JOIN users u ON u.id = o.seller_id
		WHERE o.id = $1`, orderID).
		Scan(&o.ID, &o.OrderNumber, &o.BuyerID, &o.SellerID, &o.Status, &o.Currency,
			&o.Subtotal, &o.DiscountAmount, &o.ShippingFee, &o.TotalAmount, &o.PaymentStatus,
			&o.CouponCode, &o.ShippingAddressJSON, &o.ShippingMethod, &o.Notes,
			&o.TrackingNumber, &o.Carrier, &o.PlacedAt, &o.PaidAt, &o.ShippedAt, &o.DeliveredAt, &o.CompletedAt, &o.CancelledAt,
			&o.CreatedAt, &o.UpdatedAt, &sellerName, &o.ExternalPaymentRef, &o.ExternalPaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.Seller = &domain.StoreSummary{ID: o.SellerID, Name: sellerName}
	return &o, nil
}

func (r *OrderRepository) byNumber(ctx context.Context, orderNumber string) (*domain.Order, error) {
	var o domain.Order
	var sellerName string
	err := r.pool.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.buyer_id, o.seller_id, o.status, o.currency,
		       o.subtotal, o.discount_amount, o.shipping_fee, o.total_amount, o.payment_status,
		       COALESCE(o.coupon_code,''), o.shipping_address, o.shipping_method, COALESCE(o.notes,''), COALESCE(o.tracking_number,''), COALESCE(o.carrier,''),
		       o.placed_at, o.paid_at, o.shipped_at, o.delivered_at, o.completed_at, o.cancelled_at,
		       o.created_at, o.updated_at, u.full_name,
		       COALESCE(o.external_payment_ref,''), o.external_paid_at
		FROM orders o
		JOIN users u ON u.id = o.seller_id
		WHERE o.order_number = $1`, orderNumber).
		Scan(&o.ID, &o.OrderNumber, &o.BuyerID, &o.SellerID, &o.Status, &o.Currency,
			&o.Subtotal, &o.DiscountAmount, &o.ShippingFee, &o.TotalAmount, &o.PaymentStatus,
			&o.CouponCode, &o.ShippingAddressJSON, &o.ShippingMethod, &o.Notes,
			&o.TrackingNumber, &o.Carrier, &o.PlacedAt, &o.PaidAt, &o.ShippedAt, &o.DeliveredAt, &o.CompletedAt, &o.CancelledAt,
			&o.CreatedAt, &o.UpdatedAt, &sellerName, &o.ExternalPaymentRef, &o.ExternalPaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.Seller = &domain.StoreSummary{ID: o.SellerID, Name: sellerName}
	return &o, nil
}

func (r *OrderRepository) loadItems(ctx context.Context, o *domain.Order) (*domain.Order, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, order_id, product_id, variant_id, seller_id, product_name, variant_name,
		       sku, unit_price, quantity, weight_grams, total, COALESCE(image_url,''), status, created_at
		FROM order_items WHERE order_id = $1 ORDER BY created_at`, o.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it domain.OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID, &it.SellerID,
			&it.ProductName, &it.VariantName, &it.SKU, &it.UnitPrice, &it.Quantity,
			&it.WeightGrams, &it.Total, &it.ImageURL, &it.Status, &it.CreatedAt); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, &it)
	}
	return o, rows.Err()
}

// Events returns the event timeline for an order.
func (r *OrderRepository) Events(ctx context.Context, orderID string) ([]*domain.OrderEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, order_id, from_status, to_status, actor_id, COALESCE(note,''), created_at
		FROM order_events WHERE order_id = $1 ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []*domain.OrderEvent{}
	for rows.Next() {
		var e domain.OrderEvent
		if err := rows.Scan(&e.ID, &e.OrderID, &e.FromStatus, &e.ToStatus, &e.ActorID, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

// Transition atomically moves an order to a new state with an event.
func (r *OrderRepository) Transition(ctx context.Context, orderID, from, to string, actorID *string, note string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id, note)
		SELECT id, status, $3, $4, NULLIF($5, '')
		FROM orders WHERE id = $1 AND status = $2`, orderID, from, to, actorID, note)
	return err
}

// TransitionOrder atomically records the event and updates the order status.
func (r *OrderRepository) TransitionOrder(ctx context.Context, orderID, from, to, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status = $2::varchar, updated_at = now(),
			paid_at = CASE WHEN $2::varchar = 'paid' THEN now() ELSE paid_at END,
			shipped_at = CASE WHEN $2::varchar = 'shipped' THEN now() ELSE shipped_at END,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN now() ELSE delivered_at END,
			completed_at = CASE WHEN $2::varchar = 'completed' THEN now() ELSE completed_at END,
			cancelled_at = CASE WHEN $2::varchar = 'cancelled' THEN now() ELSE cancelled_at END
		WHERE id = $1 AND status = $3::varchar`, orderID, to, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION",
			fmt.Sprintf("order is not in state %s", from))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events (order_id, from_status, to_status, actor_id)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid)`,
		orderID, from, to, actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ShipOrder marks an order shipped with tracking details.
func (r *OrderRepository) ShipOrder(ctx context.Context, orderID, from, trackingNumber, carrier string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status = 'shipped', updated_at = now(), shipped_at = now(),
			tracking_number = NULLIF($2, ''), carrier = NULLIF($3, '')
		WHERE id = $1 AND status = $4::varchar`,
		orderID, trackingNumber, carrier, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INVALID_TRANSITION",
			fmt.Sprintf("order is not in state %s", from))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events (order_id, from_status, to_status)
		VALUES ($1, $2, 'shipped')`, orderID, from); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetStatus updates the status and its timestamp.
func (r *OrderRepository) SetStatus(ctx context.Context, orderID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE orders SET status = $2::varchar, updated_at = now(),
			paid_at = CASE WHEN $2::varchar = 'paid' THEN now() ELSE paid_at END,
			shipped_at = CASE WHEN $2::varchar = 'shipped' THEN now() ELSE shipped_at END,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN now() ELSE delivered_at END,
			completed_at = CASE WHEN $2::varchar = 'completed' THEN now() ELSE completed_at END,
			cancelled_at = CASE WHEN $2::varchar = 'cancelled' THEN now() ELSE cancelled_at END
		WHERE id = $1`, orderID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetPaymentStatus updates the payment status.
func (r *OrderRepository) SetPaymentStatus(ctx context.Context, orderID, paymentStatus string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE orders SET payment_status = $2, updated_at = now() WHERE id = $1`, orderID, paymentStatus)
	return err
}

// CouponByCode loads a coupon by code.
func (r *OrderRepository) CouponByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	var c domain.Coupon
	err := r.pool.QueryRow(ctx, `
		SELECT id, code, type, value, min_subtotal, max_discount, usage_limit, used_count,
		       per_user_limit, valid_from, valid_until, is_active, seller_id
		FROM coupons WHERE lower(code) = lower($1)`, code).
		Scan(&c.ID, &c.Code, &c.Type, &c.Value, &c.MinSubtotal, &c.MaxDiscount, &c.UsageLimit,
			&c.UsedCount, &c.PerUserLimit, &c.ValidFrom, &c.ValidUntil, &c.IsActive, &c.SellerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "COUPON_NOT_FOUND", "coupon not found")
	}
	return &c, err
}

// SellerCoupons lists active coupons for a seller (My Vouchers / seller manager).
func (r *OrderRepository) SellerCoupons(ctx context.Context, sellerID string, activeOnly bool) ([]*domain.Coupon, error) {
	where := `seller_id = $1`
	if activeOnly {
		where += ` AND is_active = TRUE`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, type, value, min_subtotal, max_discount, usage_limit, used_count,
		       per_user_limit, valid_from, valid_until, is_active, seller_id
		FROM coupons WHERE `+where+` ORDER BY created_at DESC`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Coupon{}
	for rows.Next() {
		c, err := scanCoupon2(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// AllVouchers lists active store coupons (public collection page).
func (r *OrderRepository) AllVouchers(ctx context.Context) ([]*domain.Coupon, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.code, c.type, c.value, c.min_subtotal, c.max_discount, c.usage_limit, c.used_count,
		       c.per_user_limit, c.valid_from, c.valid_until, c.is_active, c.seller_id, s.name
		FROM coupons c
		LEFT JOIN stores s ON s.owner_id = c.seller_id
		WHERE c.is_active = TRUE AND c.seller_id IS NOT NULL
		  AND (c.valid_until IS NULL OR c.valid_until > now())
		ORDER BY c.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Coupon{}
	for rows.Next() {
		c, err := scanCoupon3(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func scanCoupon2(row interface{ Scan(...any) error }) (*domain.Coupon, error) {
	var c domain.Coupon
	err := row.Scan(&c.ID, &c.Code, &c.Type, &c.Value, &c.MinSubtotal, &c.MaxDiscount, &c.UsageLimit,
		&c.UsedCount, &c.PerUserLimit, &c.ValidFrom, &c.ValidUntil, &c.IsActive, &c.SellerID)
	return &c, err
}

func scanCoupon3(row interface{ Scan(...any) error }) (*domain.Coupon, error) {
	c, err := scanCoupon2(row)
	return c, err
}

// UsedCountForUser counts coupon uses by a user.
func (r *OrderRepository) UsedCountForUser(ctx context.Context, couponID, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		couponID, userID).Scan(&n)
	return n, err
}

// ListCoupons returns all coupons (admin).
func (r *OrderRepository) ListCoupons(ctx context.Context) ([]*domain.Coupon, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, type, value, min_subtotal, max_discount, usage_limit, used_count,
		       per_user_limit, valid_from, valid_until, is_active
		FROM coupons ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Coupon{}
	for rows.Next() {
		c, err := scanCoupon(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// CreateCoupon inserts a coupon (admin).
func (r *OrderRepository) CreateCoupon(ctx context.Context, c *domain.Coupon) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO coupons (id, code, type, value, min_subtotal, max_discount, usage_limit, per_user_limit, valid_from, valid_until, is_active)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), $7, $8, $9, $10, $11)`,
		c.ID, c.Code, c.Type, c.Value, c.MinSubtotal, c.MaxDiscount, c.UsageLimit,
		c.PerUserLimit, c.ValidFrom, c.ValidUntil, c.IsActive)
	return mapPgConflict(err, domain.E(domain.KindConflict, "COUPON_EXISTS", "coupon code already exists"), nil)
}

// SetCouponActive toggles a coupon (admin).
func (r *OrderRepository) SetCouponActive(ctx context.Context, couponID string, active bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE coupons SET is_active = $2 WHERE id = $1`, couponID, active)
	return err
}

// SetCouponSeller scopes an existing coupon to a seller (store coupons).
func (r *OrderRepository) SetCouponSeller(ctx context.Context, couponID, sellerID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE coupons SET seller_id = $2 WHERE id = $1`, couponID, sellerID)
	return err
}

func scanCoupon(row interface{ Scan(...any) error }) (*domain.Coupon, error) {
	var c domain.Coupon
	err := row.Scan(&c.ID, &c.Code, &c.Type, &c.Value, &c.MinSubtotal, &c.MaxDiscount, &c.UsageLimit,
		&c.UsedCount, &c.PerUserLimit, &c.ValidFrom, &c.ValidUntil, &c.IsActive)
	return &c, err
}

// ShippingMethods lists active shipping methods.
func (r *OrderRepository) ShippingMethods(ctx context.Context) ([]*domain.ShippingMethod, error) {
	return r.ShippingMethodsAll(ctx, true)
}

// ShippingMethodsAll lists shipping methods (admin: all).
func (r *OrderRepository) ShippingMethodsAll(ctx context.Context, activeOnly bool) ([]*domain.ShippingMethod, error) {
	where := ""
	if activeOnly {
		where = " WHERE is_active"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, name, base_fee, per_kg_fee, min_days, max_days, is_active
		FROM shipping_methods`+where+` ORDER BY base_fee`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	methods := []*domain.ShippingMethod{}
	for rows.Next() {
		var m domain.ShippingMethod
		if err := rows.Scan(&m.ID, &m.Code, &m.Name, &m.BaseFee, &m.PerKgFee, &m.MinDays, &m.MaxDays, &m.IsActive); err != nil {
			return nil, err
		}
		methods = append(methods, &m)
	}
	return methods, rows.Err()
}

// CreateShippingMethod adds a carrier option (admin).
func (r *OrderRepository) CreateShippingMethod(ctx context.Context, m *domain.ShippingMethod) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO shipping_methods (id, code, name, base_fee, per_kg_fee, min_days, max_days, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		m.ID, m.Code, m.Name, m.BaseFee, m.PerKgFee, m.MinDays, m.MaxDays, m.IsActive)
	return mapPgConflict(err, domain.E(domain.KindConflict, "CODE_TAKEN", "shipping code already exists"), nil)
}

// SetShippingMethodActive toggles a carrier (admin).
func (r *OrderRepository) SetShippingMethodActive(ctx context.Context, methodID string, active bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE shipping_methods SET is_active = $2 WHERE id = $1`, methodID, active)
	return err
}

// ExpiredPendingOrders lists pending orders past their payment deadline.
// Orders with a live hosted-checkout intent (e.g. an open Midtrans Snap page)
// are skipped so buyers are not cancelled mid-payment.
func (r *OrderRepository) ExpiredPendingOrders(ctx context.Context, deadline time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT o.id FROM orders o
		WHERE o.status = 'pending' AND o.placed_at < $1
		  AND NOT EXISTS (
			SELECT 1 FROM payment_intents pi
			WHERE pi.order_id = o.id
			  AND pi.gateway = 'midtrans'
			  AND pi.status = 'initiated'
			  AND pi.created_at > now() - interval '30 minutes'
		  )
		ORDER BY o.placed_at
		LIMIT $2`, deadline, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ShippedBefore lists shipped orders whose shipped_at is older than the
// cutoff — the ghost-buyer sweep marks these delivered automatically.
func (r *OrderRepository) ShippedBefore(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM orders
		WHERE status = 'shipped' AND shipped_at < $1
		ORDER BY shipped_at
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AdjustStock changes a variant's stock with a ledger reason (seller/admin).
func (r *OrderRepository) AdjustStock(ctx context.Context, variantID string, delta int, reason string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var stock int
	if err := tx.QueryRow(ctx,
		`SELECT stock FROM product_variants WHERE id = $1 FOR UPDATE`, variantID).Scan(&stock); err != nil {
		return err
	}
	if stock+delta < 0 {
		return domain.E(domain.KindConflict, "INSUFFICIENT_STOCK", "stock cannot go below zero")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE product_variants SET stock = stock + $2, updated_at = now() WHERE id = $1`,
		variantID, delta); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO stock_ledger (variant_id, change, reason) VALUES ($1, $2, $3)`,
		variantID, delta, reason)
	return tx.Commit(ctx)
}

// SearchOrders filters orders across the platform (admin).
func (r *OrderRepository) SearchOrders(ctx context.Context, q, status string, page, pageSize int) ([]*domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	where := `1=1`
	args := []any{}
	if q != "" {
		args = append(args, "%"+q+"%")
		where += fmt.Sprintf(` AND (o.order_number ILIKE $%d OR u.email ILIKE $%d OR b.full_name ILIKE $%d)`,
			len(args), len(args), len(args))
	}
	if status != "" {
		args = append(args, status)
		where += fmt.Sprintf(` AND o.status = $%d`, len(args))
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM orders o
		JOIN users u ON u.id = o.buyer_id
		JOIN users b ON b.id = o.seller_id
		WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.pool.Query(ctx, `
		SELECT o.id, o.order_number, o.buyer_id, o.seller_id, o.status, o.currency,
		       o.subtotal, o.discount_amount, o.shipping_fee, o.total_amount, o.payment_status,
		       o.coupon_code, o.shipping_address, o.shipping_method, COALESCE(o.notes,''), COALESCE(o.tracking_number,''), COALESCE(o.carrier,''),
		       o.placed_at, o.paid_at, o.shipped_at, o.delivered_at, o.completed_at, o.cancelled_at,
		       o.created_at, o.updated_at, b.full_name,
		       u.full_name,
		       COALESCE(o.external_payment_ref,''), o.external_paid_at
		FROM orders o
		JOIN users u ON u.id = o.buyer_id
		JOIN users b ON b.id = o.seller_id
		WHERE `+where+`
		ORDER BY o.created_at DESC
		LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []*domain.Order{}
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.ID, &o.OrderNumber, &o.BuyerID, &o.SellerID, &o.Status, &o.Currency,
			&o.Subtotal, &o.DiscountAmount, &o.ShippingFee, &o.TotalAmount, &o.PaymentStatus,
			&o.CouponCode, &o.ShippingAddressJSON, &o.ShippingMethod, &o.Notes,
			&o.TrackingNumber, &o.Carrier, &o.PlacedAt, &o.PaidAt, &o.ShippedAt, &o.DeliveredAt, &o.CompletedAt, &o.CancelledAt,
			&o.CreatedAt, &o.UpdatedAt, &o.SellerName, &o.BuyerName, &o.ExternalPaymentRef, &o.ExternalPaidAt); err != nil {
			return nil, 0, err
		}
		items = append(items, &o)
	}
	return items, total, rows.Err()
}

// DeliveredBefore lists delivered orders awaiting completion since a cutoff.
func (r *OrderRepository) DeliveredBefore(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM orders
		WHERE status = 'delivered' AND delivered_at < $1
		ORDER BY delivered_at
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SellerDashboard holds seller KPIs computed by the repo.
type SellerDashboard struct {
	TotalSales     float64 `json:"total_sales"`
	OrderCount     int     `json:"order_count"`
	PendingOrders  int     `json:"pending_orders"`
	ProductsActive int     `json:"products_active"`
	ProductsDraft  int     `json:"products_draft"`
}

// Dashboard aggregates seller KPIs.
func (r *OrderRepository) Dashboard(ctx context.Context, sellerID string) (*SellerDashboard, error) {
	d := &SellerDashboard{}
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_amount), 0), COUNT(*)::int,
		       COUNT(*) FILTER (WHERE status = 'pending')::int
		FROM orders WHERE seller_id = $1 AND status NOT IN ('cancelled')`, sellerID).
		Scan(&d.TotalSales, &d.OrderCount, &d.PendingOrders); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'active')::int, COUNT(*) FILTER (WHERE status = 'draft')::int
		FROM products WHERE seller_id = $1`, sellerID).
		Scan(&d.ProductsActive, &d.ProductsDraft); err != nil {
		return nil, err
	}
	return d, nil
}

// Scan helpers ---

type orderRow interface {
	Scan(dest ...any) error
}

func scanOrder(row orderRow) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(&o.ID, &o.OrderNumber, &o.BuyerID, &o.SellerID, &o.Status, &o.Currency,
		&o.Subtotal, &o.DiscountAmount, &o.ShippingFee, &o.TotalAmount, &o.PaymentStatus,
		&o.CouponCode, &o.ShippingAddressJSON, &o.ShippingMethod, &o.Notes,
		&o.TrackingNumber, &o.Carrier, &o.PlacedAt, &o.PaidAt, &o.ShippedAt, &o.DeliveredAt, &o.CompletedAt, &o.CancelledAt,
		&o.CreatedAt, &o.UpdatedAt, &o.SellerName, &o.ExternalPaymentRef, &o.ExternalPaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// DailyDigest is yesterday's summary for one seller.
type DailyDigest struct {
	OrderCount  int
	GMV         float64
	TopProduct  string
	ReviewCount int
}

// DailyDigestFor computes yesterday's sales snapshot for a seller.
func (r *OrderRepository) DailyDigestFor(ctx context.Context, sellerID string, from, to time.Time) (*DailyDigest, error) {
	d := &DailyDigest{}
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(COUNT(*)::int, 0), COALESCE(SUM(o.total_amount), 0)::float8
		FROM orders o
		WHERE o.seller_id = $1 AND o.created_at >= $2 AND o.created_at < $3
		  AND o.status NOT IN ('cancelled')`, sellerID, from, to).
		Scan(&d.OrderCount, &d.GMV); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT oi.product_name
		                 FROM order_items oi
		                 JOIN orders o ON o.id = oi.order_id
		                 WHERE oi.seller_id = $1 AND o.created_at >= $2 AND o.created_at < $3
		                 GROUP BY oi.product_name
		                 ORDER BY SUM(oi.quantity) DESC
		                 LIMIT 1), '')`, sellerID, from, to).Scan(&d.TopProduct); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM product_reviews rv
		JOIN products p ON p.id = rv.product_id
		WHERE p.seller_id = $1 AND rv.status = 'approved' AND rv.created_at >= $2 AND rv.created_at < $3`,
		sellerID, from, to).Scan(&d.ReviewCount); err != nil {
		return nil, err
	}
	return d, nil
}

// AddressFromJSON decodes the stored shipping address.
func AddressFromJSON(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
