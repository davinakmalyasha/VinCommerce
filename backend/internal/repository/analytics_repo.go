package repository

import (
	"context"
	"time"

	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// AnalyticsRepository computes platform and seller reports.
type AnalyticsRepository struct {
	pool *db.Pool
}

// NewAnalyticsRepository creates an AnalyticsRepository.
func NewAnalyticsRepository(pool *db.Pool) *AnalyticsRepository {
	return &AnalyticsRepository{pool: pool}
}

// PendingCounts aggregates moderation-queue sizes for the ops dashboard.
type PendingCounts struct {
	Stores   int64 `json:"stores"`
	KYC      int64 `json:"kyc"`
	Reviews  int64 `json:"reviews"`
	Returns  int64 `json:"returns"`
	Disputes int64 `json:"disputes"`
	Tickets  int64 `json:"tickets"`
}

// PendingCounts returns one row of queue sizes (single round-trip).
func (r *AnalyticsRepository) PendingCounts(ctx context.Context) (*PendingCounts, error) {
	var c PendingCounts
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM stores WHERE status = 'pending'),
			(SELECT COUNT(*) FROM seller_kyc WHERE status = 'pending'),
			(SELECT COUNT(*) FROM product_reviews WHERE status = 'pending'),
			(SELECT COUNT(*) FROM return_requests WHERE status = 'requested'),
			(SELECT COUNT(*) FROM disputes WHERE status = 'open'),
			(SELECT COUNT(*) FROM support_tickets WHERE status NOT IN ('resolved', 'closed'))`).
		Scan(&c.Stores, &c.KYC, &c.Reviews, &c.Returns, &c.Disputes, &c.Tickets)
	return &c, err
}

// SalesPoint is one day's aggregated sales.
type SalesPoint struct {
	Day        time.Time  `json:"day"`
	GMV        domain.IDR `json:"gmv"`
	OrderCount int        `json:"order_count"`
	ItemCount  int        `json:"item_count"`
}

// SalesSeries aggregates sales per day within a range.
func (r *AnalyticsRepository) SalesSeries(ctx context.Context, from, to time.Time, sellerID string) ([]*SalesPoint, error) {
	args := []any{from, to}
	where := `o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')`
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND o.seller_id = $3`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT date_trunc('day', o.created_at)::date AS day,
		       COALESCE(SUM(o.total_amount), 0),
		       COUNT(*)::int,
		       COALESCE(SUM((SELECT COALESCE(SUM(quantity), 0) FROM order_items oi WHERE oi.order_id = o.id)), 0)::int
		FROM orders o
		WHERE `+where+`
		GROUP BY day
		ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := []*SalesPoint{}
	for rows.Next() {
		var p SalesPoint
		var gmv int64
		if err := rows.Scan(&p.Day, &gmv, &p.OrderCount, &p.ItemCount); err != nil {
			return nil, err
		}
		p.GMV = domain.IDR(gmv)
		points = append(points, &p)
	}
	return points, rows.Err()
}

// Summary is the platform/seller overview snapshot.
type Summary struct {
	GMV          domain.IDR `json:"gmv"`
	OrderCount   int        `json:"order_count"`
	BuyerCount   int        `json:"buyer_count"`
	ProductCount int        `json:"product_count"`
	StoreCount   int        `json:"store_count"`
	AvgOrder     domain.IDR `json:"avg_order"`
}

// Overview computes the headline metrics.
func (r *AnalyticsRepository) Overview(ctx context.Context, from, to time.Time, sellerID string) (*Summary, error) {
	args := []any{from, to}
	where := `o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')`
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND o.seller_id = $3`
	}

	s := &Summary{}
	var gmv, avg int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(o.total_amount), 0), COUNT(*)::int,
		       COUNT(DISTINCT o.buyer_id)::int,
		       COALESCE(SUM(o.total_amount) / NULLIF(COUNT(*), 0), 0)
		FROM orders o WHERE `+where, args...).
		Scan(&gmv, &s.OrderCount, &s.BuyerCount, &avg); err != nil {
		return nil, err
	}
	s.GMV, s.AvgOrder = domain.IDR(gmv), domain.IDR(avg)

	if sellerID == "" {
		if err := r.pool.QueryRow(ctx, `
			SELECT (SELECT COUNT(*) FROM products WHERE status = 'active'),
			       (SELECT COUNT(*) FROM stores WHERE status = 'active')`).
			Scan(&s.ProductCount, &s.StoreCount); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// TopProduct is a bestseller row.
type TopProduct struct {
	ProductID string     `json:"product_id"`
	Name      string     `json:"name"`
	Quantity  int        `json:"quantity"`
	Revenue   domain.IDR `json:"revenue"`
}

// TopProducts ranks products by quantity sold.
func (r *AnalyticsRepository) TopProducts(ctx context.Context, from, to time.Time, sellerID string, limit int) ([]*TopProduct, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	args := []any{from, to, limit}
	where := `o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')`
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND oi.seller_id = $4`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT oi.product_id, oi.product_name, SUM(oi.quantity)::int, SUM(oi.total)
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		WHERE `+where+`
		GROUP BY oi.product_id, oi.product_name
		ORDER BY SUM(oi.quantity) DESC
		LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*TopProduct{}
	for rows.Next() {
		var p TopProduct
		var revenue int64
		if err := rows.Scan(&p.ProductID, &p.Name, &p.Quantity, &revenue); err != nil {
			return nil, err
		}
		p.Revenue = domain.IDR(revenue)
		items = append(items, &p)
	}
	return items, rows.Err()
}

// RecordProductView logs a product view (optional user).
func (r *AnalyticsRepository) RecordProductView(ctx context.Context, productID string, userID *string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO product_views (product_id, user_id) VALUES ($1, $2)`, productID, userID)
	return err
}

// ViewedProduct is one product the user recently looked at, with just enough
// to render a card and a link. Inactive products are included with their
// status: a history that silently drops a viewed product reads as data loss,
// and the product page already handles inactive states.
type ViewedProduct struct {
	ProductID string     `json:"product_id"`
	Slug      string     `json:"slug"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	ImageURL  string     `json:"image_url,omitempty"`
	Price     domain.IDR `json:"price"`
	ViewedAt  time.Time  `json:"viewed_at"`
}

// RecentViews lists the user's most recently viewed products, newest first.
func (r *AnalyticsRepository) RecentViews(ctx context.Context, userID string, limit int) ([]*ViewedProduct, error) {
	if limit < 1 || limit > 30 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT v.product_id, p.slug, p.name, p.status,
		       COALESCE((SELECT url FROM product_images WHERE product_id = p.id ORDER BY is_primary DESC, position LIMIT 1), ''),
		       COALESCE((SELECT price FROM product_variants WHERE product_id = p.id AND is_active ORDER BY price LIMIT 1), 0),
		       MAX(v.created_at) AS viewed_at
		FROM product_views v
		JOIN products p ON p.id = v.product_id
		WHERE v.user_id = $1
		GROUP BY v.product_id, p.slug, p.name, p.status
		ORDER BY viewed_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ViewedProduct{}
	for rows.Next() {
		var item ViewedProduct
		var price int64
		if err := rows.Scan(&item.ProductID, &item.Slug, &item.Name, &item.Status,
			&item.ImageURL, &price, &item.ViewedAt); err != nil {
			return nil, err
		}
		item.Price = domain.IDR(price)
		out = append(out, &item)
	}
	return out, rows.Err()
}

// FunnelStep is one stage of the conversion funnel.
type FunnelStep struct {
	Label string  `json:"label"`
	Count int     `json:"count"`
	Rate  float64 `json:"rate"`
}

// CommissionEarned sums platform commission income (escrow releases) in the range.
func (r *AnalyticsRepository) CommissionEarned(ctx context.Context, from, to time.Time) (domain.IDR, error) {
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM wallet_transactions
		WHERE reason = 'commission' AND created_at >= $1 AND created_at < $2`,
		from, to).Scan(&total)
	return domain.IDR(total), err
}

// TopSeller is GMV per seller.
type TopSeller struct {
	SellerID string     `json:"seller_id"`
	Name     string     `json:"name"`
	GMV      domain.IDR `json:"gmv"`
	Orders   int        `json:"orders"`
}

// TopSellers ranks sellers by GMV in the range.
func (r *AnalyticsRepository) TopSellers(ctx context.Context, from, to time.Time, limit int) ([]*TopSeller, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx, `
		SELECT o.seller_id, u.full_name, SUM(o.total_amount), COUNT(*)::int
		FROM orders o
		JOIN users u ON u.id = o.seller_id
		WHERE o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')
		GROUP BY o.seller_id, u.full_name
		ORDER BY SUM(o.total_amount) DESC
		LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*TopSeller{}
	for rows.Next() {
		var s TopSeller
		var gmv int64
		if err := rows.Scan(&s.SellerID, &s.Name, &gmv, &s.Orders); err != nil {
			return nil, err
		}
		s.GMV = domain.IDR(gmv)
		items = append(items, &s)
	}
	return items, rows.Err()
}

// CouponStat is redemption count per coupon.
type CouponStat struct {
	Code      string  `json:"code"`
	Type      string  `json:"type"`
	Value     float64 `json:"value"`
	UsedCount int     `json:"used_count"`
}

// CouponStats ranks coupons by redemptions in the range.
func (r *AnalyticsRepository) CouponStats(ctx context.Context, from, to time.Time, limit int) ([]*CouponStat, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.code, c.type, c.value, COUNT(*)::int
		FROM coupon_usages cu
		JOIN coupons c ON c.id = cu.coupon_id
		WHERE cu.used_at >= $1 AND cu.used_at < $2
		GROUP BY c.code, c.type, c.value
		ORDER BY COUNT(*) DESC
		LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*CouponStat{}
	for rows.Next() {
		var s CouponStat
		if err := rows.Scan(&s.Code, &s.Type, &s.Value, &s.UsedCount); err != nil {
			return nil, err
		}
		items = append(items, &s)
	}
	return items, rows.Err()
}

// Funnel computes views → cart sessions → orders → paid orders.
func (r *AnalyticsRepository) Funnel(ctx context.Context, from, to time.Time, sellerID string) ([]*FunnelStep, error) {
	args := []any{from, to, sellerID}
	viewsWhere := `pv.created_at >= $1 AND pv.created_at < $2
		AND ($3::text = '' OR pv.product_id IN (SELECT id FROM products WHERE seller_id::text = $3::text))`

	var views, carts, orders, paid int
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM product_views pv WHERE `+viewsWhere, args...).Scan(&views); err != nil {
		return nil, err
	}

	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT c.id)
		FROM carts c
		JOIN cart_items ci ON ci.cart_id = c.id
		JOIN product_variants v ON v.id = ci.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE c.created_at >= $1 AND c.created_at < $2
		  AND ($3::text = '' OR p.seller_id::text = $3::text)`, args...).Scan(&carts); err != nil {
		return nil, err
	}

	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM orders o
		WHERE o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')
		  AND ($3::text = '' OR o.seller_id::text = $3::text)`, args...).Scan(&orders); err != nil {
		return nil, err
	}

	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM orders o
		WHERE o.created_at >= $1 AND o.created_at < $2
		  AND o.status NOT IN ('cancelled') AND o.payment_status = 'paid'
		  AND ($3::text = '' OR o.seller_id::text = $3::text)`, args...).Scan(&paid); err != nil {
		return nil, err
	}

	rate := func(n, d int) float64 {
		if d == 0 {
			return 0
		}
		return float64(n) / float64(d) * 100
	}
	return []*FunnelStep{
		{Label: "Kunjungan produk", Count: views, Rate: 100},
		{Label: "Keranjang", Count: carts, Rate: rate(carts, views)},
		{Label: "Pesanan", Count: orders, Rate: rate(orders, views)},
		{Label: "Pesanan dibayar", Count: paid, Rate: rate(paid, views)},
	}, nil
}

// CategorySales is revenue by category.
type CategorySales struct {
	Category string     `json:"category"`
	Revenue  domain.IDR `json:"revenue"`
	Quantity int        `json:"quantity"`
}

// CategorySalesBy aggregates order revenue per product category.
func (r *AnalyticsRepository) CategorySalesBy(ctx context.Context, from, to time.Time, sellerID string) ([]*CategorySales, error) {
	args := []any{from, to}
	where := `o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')`
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND oi.seller_id = $3`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(c.name, 'Tanpa kategori'), SUM(oi.total), SUM(oi.quantity)::int
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		LEFT JOIN products p ON p.id = oi.product_id
		LEFT JOIN categories c ON c.id = p.category_id
		WHERE `+where+`
		GROUP BY c.name
		ORDER BY SUM(oi.total) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*CategorySales{}
	for rows.Next() {
		var c CategorySales
		var revenue int64
		if err := rows.Scan(&c.Category, &revenue, &c.Quantity); err != nil {
			return nil, err
		}
		c.Revenue = domain.IDR(revenue)
		items = append(items, &c)
	}
	return items, rows.Err()
}

// PaymentSplit is GMV per payment method.
type PaymentSplit struct {
	Method  string     `json:"method"`
	Revenue domain.IDR `json:"revenue"`
	Count   int        `json:"count"`
}

// PaymentSplitBy aggregates paid GMV per payment method (platform-wide).
func (r *AnalyticsRepository) PaymentSplitBy(ctx context.Context, from, to time.Time) ([]*PaymentSplit, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(pi.method, 'unknown'), SUM(pi.amount), COUNT(*)::int
		FROM payment_intents pi
		WHERE pi.status = 'captured'
		  AND pi.created_at >= $1 AND pi.created_at < $2
		GROUP BY pi.method
		ORDER BY SUM(pi.amount) DESC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*PaymentSplit{}
	for rows.Next() {
		var p PaymentSplit
		var revenue int64
		if err := rows.Scan(&p.Method, &revenue, &p.Count); err != nil {
			return nil, err
		}
		p.Revenue = domain.IDR(revenue)
		items = append(items, &p)
	}
	return items, rows.Err()
}

// BuyerCohort splits buyers into new vs returning within the range.
type BuyerCohort struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// BuyerCohorts counts first-time vs repeat buyers in the range.
func (r *AnalyticsRepository) BuyerCohorts(ctx context.Context, from, to time.Time, sellerID string) ([]*BuyerCohort, error) {
	args := []any{from, to}
	where := `o.created_at >= $1 AND o.created_at < $2 AND o.status NOT IN ('cancelled')`
	if sellerID != "" {
		args = append(args, sellerID)
		where += ` AND o.seller_id = $3`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT CASE
		         WHEN EXISTS (SELECT 1 FROM orders prev WHERE prev.buyer_id = o.buyer_id AND prev.created_at < $1 AND prev.status NOT IN ('cancelled'))
		         THEN 'returning' ELSE 'new' END AS cohort,
		       COUNT(DISTINCT o.buyer_id)::int
		FROM orders o
		WHERE `+where+`
		GROUP BY cohort`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*BuyerCohort{}
	for rows.Next() {
		var c BuyerCohort
		if err := rows.Scan(&c.Label, &c.Count); err != nil {
			return nil, err
		}
		items = append(items, &c)
	}
	return items, rows.Err()
}
