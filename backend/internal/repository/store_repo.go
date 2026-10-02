package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// StoreRepository persists stores, KYC and return requests.
type StoreRepository struct {
	pool *db.Pool
}

// NewStoreRepository creates a StoreRepository.
func NewStoreRepository(pool *db.Pool) *StoreRepository {
	return &StoreRepository{pool: pool}
}

// Pool exposes the pool, so a service holding only a StoreRepository can still reach
// the database for the return columns it needs.
//
// Added with the return-parcel work: `stores` had no accessor, so a service that
// needed `return_address` had no way to read it without the repository growing a
// method per call site. The other repositories (Order, Payment, Shipment, Ledger)
// all have this.
func (r *StoreRepository) Pool() *db.Pool { return r.pool }

// FreeShippingThreshold returns the seller's free-shipping threshold (nil if unset).
func (r *StoreRepository) FreeShippingThreshold(ctx context.Context, ownerID string) (*float64, error) {
	var t *float64
	err := r.pool.QueryRow(ctx,
		`SELECT free_shipping_threshold FROM stores WHERE owner_id = $1`, ownerID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_STORE", "seller has no store")
	}
	return t, err
}

// SetFreeShippingThreshold updates the store's threshold.
func (r *StoreRepository) SetFreeShippingThreshold(ctx context.Context, ownerID string, threshold *float64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE stores SET free_shipping_threshold = NULLIF($2, 0), updated_at = now() WHERE owner_id = $1`,
		ownerID, threshold)
	return err
}

// PayoutLagDays returns this seller's payout lag override, or nil when they have
// chosen none.
//
// nil MEANS "use the platform default", and the distinction from "chose seven" is
// deliberate: a default on the column would make "seven" permanent and a future
// change to `PAYOUT_LAG_DAYS` would silently skip every seller. NULL means the
// seller never chose.
//
// Not on `domain.Store`, for the same reason `free_shipping_threshold` is not: it
// is read once per release run for one seller, not as part of a store listing, and
// putting it on the struct would add a column to all seven store queries. The
// convention this file already uses for a nullable numeric is a `*T` scanned
// directly, which preserves SQL NULL all the way to the caller.
func (r *StoreRepository) PayoutLagDays(ctx context.Context, ownerID string) (*int, error) {
	var days *int
	err := r.pool.QueryRow(ctx,
		`SELECT payout_lag_days FROM stores WHERE owner_id = $1`, ownerID).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_STORE", "seller has no store")
	}
	return days, err
}

// SetPayoutLagDays sets or clears this seller's payout lag override.
//
// Unlike `SetFreeShippingThreshold` this does NOT check RowsAffected and does NOT
// translate a miss into ErrNotFound. A free-shipping threshold is set by the seller
// about their own store, so a miss is worth reporting; a payout lag is set by an
// operator against a store id, and the service resolves the store first -- so a miss
// here means the store was deleted between the two calls, which the caller has
// already been told about.
//
// `NULLIF($2, 0)` for the same reason as the threshold: 0 is not a lag (see the
// migration's CHECK), so passing it clears the override rather than storing a value
// the release query would treat as "release immediately".
func (r *StoreRepository) SetPayoutLagDays(ctx context.Context, ownerID string, days *int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE stores SET payout_lag_days = NULLIF($2, 0), updated_at = now() WHERE owner_id = $1`,
		ownerID, days)
	return err
}

// CategoryBySlug resolves a category id from its slug (bulk import helper)
func (r *StoreRepository) CategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	var c domain.Category
	err := r.pool.QueryRow(ctx, `
		SELECT id, parent_id, name, slug, path, depth, position, is_active
		FROM categories WHERE slug = $1`, slug).
		Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &c.Depth, &c.Position, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &c, err
}

// Create registers a pending store for the seller.
func (r *StoreRepository) Create(ctx context.Context, s *domain.Store) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO stores (id, owner_id, name, slug, description, logo_url, banner_url)
		VALUES ($1, $2, $3, $4, COALESCE($5, ''), NULLIF($6, ''), NULLIF($7, ''))
		RETURNING joined_at`,
		s.ID, s.OwnerID, s.Name, s.Slug, s.Description, s.LogoURL, s.BannerURL).Scan(&s.JoinedAt)
	if err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "store slug is taken"),
			domain.E(domain.KindConflict, "STORE_EXISTS", "you already have a store"))
	}
	return nil
}

// DeleteIfOwner removes a store owned by userID (compensation path for
// failed onboarding role grants).
func (r *StoreRepository) DeleteIfOwner(ctx context.Context, storeID, ownerID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM stores WHERE id = $1 AND owner_id = $2`, storeID, ownerID)
	return err
}

// ByID fetches a store by id (any status).
func (r *StoreRepository) ByID(ctx context.Context, storeID string) (*domain.Store, error) {
	var s domain.Store
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, slug, COALESCE(description,''), COALESCE(logo_url,''),
		       COALESCE(banner_url,''), status, rating, rating_count, products_count, follower_count, joined_at, updated_at
		FROM stores WHERE id = $1`, storeID).
		Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

// FollowerCountOf returns the store's follower count.
func (r *StoreRepository) FollowerCountOf(ctx context.Context, storeID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT follower_count FROM stores WHERE id = $1`, storeID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return n, err
}

// ByOwner fetches the store for a user.
func (r *StoreRepository) ByOwner(ctx context.Context, ownerID string) (*domain.Store, error) {
	var s domain.Store
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, slug, COALESCE(description,''), COALESCE(logo_url,''),
		       COALESCE(banner_url,''), status, rating, rating_count, products_count, follower_count, joined_at, updated_at
		FROM stores WHERE owner_id = $1`, ownerID).
		Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_STORE", "seller has no store")
	}
	return &s, err
}

// BySlug fetches a public store profile.
func (r *StoreRepository) BySlug(ctx context.Context, slug string) (*domain.Store, error) {
	var s domain.Store
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, slug, COALESCE(description,''), COALESCE(logo_url,''),
		       COALESCE(banner_url,''), status, rating, rating_count, products_count, follower_count, joined_at, updated_at
		FROM stores WHERE slug = $1 AND status = 'active'`, slug).
		Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

// SetStatus updates store status (approve/suspend/reject).
func (r *StoreRepository) SetStatus(ctx context.Context, storeID, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE stores SET status = $2, updated_at = now() WHERE id = $1`, storeID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Follow records a store follow (idempotent).
func (r *StoreRepository) Follow(ctx context.Context, userID, storeID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO store_follows (user_id, store_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, storeID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE stores SET follower_count = follower_count + 1 WHERE id = $2 AND EXISTS (SELECT 1 FROM store_follows WHERE user_id = $1 AND store_id = $2)`,
		userID, storeID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Unfollow removes a store follow (idempotent).
func (r *StoreRepository) Unfollow(ctx context.Context, userID, storeID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var followed bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM store_follows WHERE user_id = $1 AND store_id = $2)`,
		userID, storeID).Scan(&followed); err != nil {
		return err
	}
	if !followed {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM store_follows WHERE user_id = $1 AND store_id = $2`, userID, storeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE stores SET follower_count = GREATEST(follower_count - 1, 0) WHERE id = $1`, storeID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsFollowing reports whether the user follows the store.
func (r *StoreRepository) IsFollowing(ctx context.Context, userID, storeID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM store_follows WHERE user_id = $1 AND store_id = $2)`,
		userID, storeID).Scan(&ok)
	return ok, err
}

// FollowedStores lists active stores the user follows, newest first.
func (r *StoreRepository) FollowedStores(ctx context.Context, userID string) ([]*domain.Store, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.owner_id, s.name, s.slug, COALESCE(s.description,''), COALESCE(s.logo_url,''),
		       COALESCE(s.banner_url,''), s.status, s.rating, s.rating_count, s.products_count, s.follower_count, s.joined_at, s.updated_at
		FROM store_follows f
		JOIN stores s ON s.id = f.store_id
		WHERE f.user_id = $1 AND s.status = 'active'
		ORDER BY f.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stores := []*domain.Store{}
	for rows.Next() {
		var s domain.Store
		if err := rows.Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		stores = append(stores, &s)
	}
	return stores, rows.Err()
}

// FollowersOf lists user ids following the store (notification fan-out).
func (r *StoreRepository) FollowersOf(ctx context.Context, storeID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM store_follows WHERE store_id = $1`, storeID)
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

// RecentProductsFromFollowed lists the newest active products from stores the
// user follows (discovery feed).
func (r *StoreRepository) RecentProductsFromFollowed(ctx context.Context, userID string, limit int) ([]*domain.Product, error) {
	if limit < 1 || limit > 30 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.slug, p.name, p.avg_rating, p.rating_count, p.sold_count,
		       (SELECT MIN(v.price) FROM product_variants v WHERE v.product_id = p.id AND v.is_active),
		       (SELECT v.image_url FROM product_variants v WHERE v.product_id = p.id AND v.is_active AND v.image_url IS NOT NULL LIMIT 1)
		FROM store_follows f
		JOIN stores s ON s.id = f.store_id
		JOIN products p ON p.seller_id = s.owner_id
		WHERE f.user_id = $1 AND p.status = 'active'
		ORDER BY p.published_at DESC, p.created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Product{}
	for rows.Next() {
		var p domain.Product
		var minPrice *float64
		var imageURL *string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.AvgRating, &p.RatingCount, &p.SoldCount,
			&minPrice, &imageURL); err != nil {
			return nil, err
		}
		if minPrice != nil {
			p.Variants = []*domain.ProductVariant{{Price: *minPrice}}
		}
		if imageURL != nil && *imageURL != "" {
			p.Images = []*domain.ProductImage{{URL: *imageURL, IsPrimary: true}}
		}
		items = append(items, &p)
	}
	return items, rows.Err()
}

// ActiveStores lists all active stores (daily digest fan-out).
func (r *StoreRepository) ActiveStores(ctx context.Context) ([]*domain.Store, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, owner_id, name, slug, COALESCE(description,''), COALESCE(logo_url,''),
		       COALESCE(banner_url,''), status, rating, rating_count, products_count, follower_count, joined_at, updated_at
		FROM stores WHERE status = 'active' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stores := []*domain.Store{}
	for rows.Next() {
		var s domain.Store
		if err := rows.Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		stores = append(stores, &s)
	}
	return stores, rows.Err()
}

// Update updates store branding.
func (r *StoreRepository) Update(ctx context.Context, s *domain.Store) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE stores SET name = $2, description = NULLIF($3, ''), logo_url = NULLIF($4, ''),
		                  banner_url = NULLIF($5, ''), updated_at = now()
		WHERE id = $1`,
		s.ID, s.Name, s.Description, s.LogoURL, s.BannerURL)
	return err
}

// PendingStores lists stores awaiting approval.
func (r *StoreRepository) PendingStores(ctx context.Context) ([]*domain.Store, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, owner_id, name, slug, COALESCE(description,''), COALESCE(logo_url,''),
		       COALESCE(banner_url,''), status, rating, rating_count, products_count, follower_count, joined_at, updated_at
		FROM stores WHERE status = 'pending' ORDER BY joined_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stores := []*domain.Store{}
	for rows.Next() {
		var s domain.Store
		if err := rows.Scan(&s.ID, &s.OwnerID, &s.Name, &s.Slug, &s.Description, &s.LogoURL,
			&s.BannerURL, &s.Status, &s.Rating, &s.RatingCount, &s.ProductsCount, &s.FollowerCount, &s.JoinedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		stores = append(stores, &s)
	}
	return stores, rows.Err()
}

// SaveKYC upserts the KYC record.
func (r *StoreRepository) SaveKYC(ctx context.Context, k *domain.SellerKYC) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO seller_kyc (id, store_id, owner_name, id_number, id_document_url, bank_name, bank_account)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
		ON CONFLICT (store_id) DO UPDATE SET
			owner_name = EXCLUDED.owner_name, id_number = EXCLUDED.id_number,
			id_document_url = EXCLUDED.id_document_url, bank_name = EXCLUDED.bank_name,
			bank_account = EXCLUDED.bank_account, status = 'pending', admin_note = NULL,
			updated_at = now()`,
		k.ID, k.StoreID, k.OwnerName, k.IDNumber, k.IDDocumentURL, k.BankName, k.BankAccount)
	return err
}

// KYCByStore fetches KYC for a store.
func (r *StoreRepository) KYCByStore(ctx context.Context, storeID string) (*domain.SellerKYC, error) {
	var k domain.SellerKYC
	err := r.pool.QueryRow(ctx, `
		SELECT id, store_id, owner_name, id_number, COALESCE(id_document_url,''),
		       bank_name, bank_account, status, COALESCE(admin_note,''), reviewed_at, created_at
		FROM seller_kyc WHERE store_id = $1`, storeID).
		Scan(&k.ID, &k.StoreID, &k.OwnerName, &k.IDNumber, &k.IDDocumentURL,
			&k.BankName, &k.BankAccount, &k.Status, &k.AdminNote, &k.ReviewedAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_KYC", "KYC not submitted")
	}
	return &k, err
}

// SetKYCStatus approves or rejects KYC.
func (r *StoreRepository) SetKYCStatus(ctx context.Context, storeID, status, note string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE seller_kyc SET status = $2, admin_note = NULLIF($3, ''), reviewed_at = now(), updated_at = now()
		WHERE store_id = $1`, storeID, status, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// StoreKYC is a KYC submission enriched with the store name (admin review).
type StoreKYC struct {
	domain.SellerKYC
	StoreName string `json:"store_name"`
}

// PresenceStats are chat responsiveness signals for one store.
type PresenceStats struct {
	ResponseRatePct *int `json:"response_rate_pct,omitempty"`
	AvgReplyMinutes *int `json:"avg_reply_minutes,omitempty"`
}

// PresenceByStore loads computed chat stats (nil columns when insufficient data).
func (r *StoreRepository) PresenceByStore(ctx context.Context, storeID string) (*PresenceStats, error) {
	var p PresenceStats
	err := r.pool.QueryRow(ctx,
		`SELECT response_rate_pct, avg_reply_minutes FROM stores WHERE id = $1`, storeID).
		Scan(&p.ResponseRatePct, &p.AvgReplyMinutes)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdatePresenceStats recomputes per-store chat responsiveness over the last
// 30 days of seller-type sessions (nightly job).
func (r *StoreRepository) UpdatePresenceStats(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
WITH asked AS (
    SELECT cs.id AS session_id, o.seller_id,
           (SELECT MIN(m.created_at) FROM chat_messages m
            WHERE m.session_id = cs.id AND m.sender_role = 'customer') AS asked_at
    FROM chat_sessions cs
    JOIN orders o ON o.id = cs.order_id
    WHERE cs.type = 'seller' AND cs.created_at > now() - interval '30 days'
),
base AS (
    SELECT a.seller_id AS store_id, a.asked_at,
           (SELECT MIN(m.created_at) FROM chat_messages m
            WHERE m.session_id = a.session_id AND m.sender_role <> 'customer'
              AND m.created_at >= a.asked_at) AS replied_at
    FROM asked a
    WHERE a.asked_at IS NOT NULL
),
agg AS (
    SELECT store_id,
           ROUND(100.0 * COUNT(replied_at) / GREATEST(COUNT(*), 1))::int AS rate,
           AVG(EXTRACT(EPOCH FROM (replied_at - asked_at)) / 60)::int AS avg_minutes
    FROM base
    GROUP BY store_id
)
UPDATE stores st SET response_rate_pct = a.rate, avg_reply_minutes = a.avg_minutes
FROM agg a WHERE st.id = a.store_id`)
	return err
}

// PendingKYC lists KYC submissions awaiting review.
func (r *StoreRepository) PendingKYC(ctx context.Context) ([]*StoreKYC, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT k.id, k.store_id, k.owner_name, k.id_number, COALESCE(k.id_document_url,''),
		       k.bank_name, k.bank_account, k.status, COALESCE(k.admin_note,''), k.reviewed_at, k.created_at,
		       s.name
		FROM seller_kyc k
		JOIN stores s ON s.id = k.store_id
		WHERE k.status = 'pending'
		ORDER BY k.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []*StoreKYC{}
	for rows.Next() {
		var kc StoreKYC
		if err := rows.Scan(&kc.ID, &kc.StoreID, &kc.OwnerName, &kc.IDNumber, &kc.IDDocumentURL,
			&kc.BankName, &kc.BankAccount, &kc.Status, &kc.AdminNote, &kc.ReviewedAt, &kc.CreatedAt,
			&kc.StoreName); err != nil {
			return nil, err
		}
		list = append(list, &kc)
	}
	return list, rows.Err()
}

// CreateReturn registers a return request.
func (r *StoreRepository) CreateReturn(ctx context.Context, req *domain.ReturnRequest) error {
	if req.EvidenceURLs == nil {
		req.EvidenceURLs = []string{}
	}
	if req.IssueType == "" {
		req.IssueType = "return"
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO return_requests (id, order_id, order_item_id, buyer_id, seller_id, issue_type, reason, description, evidence_urls, status, resolution)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''))
		RETURNING requested_at`,
		req.ID, req.OrderID, req.OrderItemID, req.BuyerID, req.SellerID, req.IssueType,
		req.Reason, req.Description, req.EvidenceURLs, req.Status, req.Resolution).
		Scan(&req.RequestedAt)
	return err
}

// ReturnByID fetches a return request with item info.
func (r *StoreRepository) ReturnByID(ctx context.Context, returnID string) (*domain.ReturnRequest, error) {
	req, err := r.returnBy(ctx, `WHERE r.id = $1`, returnID)
	if err != nil {
		return nil, err
	}
	return r.loadReturnAmount(ctx, req)
}

// ReturnsByBuyer lists a buyer's return requests.
func (r *StoreRepository) ReturnsByBuyer(ctx context.Context, buyerID string) ([]*domain.ReturnRequest, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.order_id, r.order_item_id, r.buyer_id, r.seller_id, r.reason, r.description,
		       r.evidence_urls, r.status, COALESCE(r.resolution,''), COALESCE(r.seller_note,''),
		       COALESCE(r.admin_note,''), r.requested_at, r.resolved_at, oi.product_name
		FROM return_requests r
		JOIN order_items oi ON oi.id = r.order_item_id
		WHERE r.buyer_id = $1 ORDER BY r.requested_at DESC`, buyerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.ReturnRequest{}
	for rows.Next() {
		req, err := scanReturn(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, req)
	}
	return items, rows.Err()
}

// ReturnsBySeller lists return requests for a seller.
func (r *StoreRepository) ReturnsBySeller(ctx context.Context, sellerID, status string) ([]*domain.ReturnRequest, error) {
	where := `r.seller_id = $1`
	args := []any{sellerID}
	if status != "" {
		args = append(args, status)
		where += ` AND r.status = $2`
	}
	return r.returns(ctx, where, args)
}

// ReturnsByStatus lists return requests by status (admin).
func (r *StoreRepository) ReturnsByStatus(ctx context.Context, status string) ([]*domain.ReturnRequest, error) {
	where := `r.status = $1`
	if status == "" || status == "all" {
		where = `r.status <> 'closed'`
		return r.returns(ctx, where, nil)
	}
	return r.returns(ctx, where, []any{status})
}

func (r *StoreRepository) returns(ctx context.Context, where string, args []any) ([]*domain.ReturnRequest, error) {
	query := `
		SELECT r.id, r.order_id, r.order_item_id, r.buyer_id, r.seller_id, r.reason, r.description,
		       r.evidence_urls, r.status, COALESCE(r.resolution,''), COALESCE(r.seller_note,''),
		       COALESCE(r.admin_note,''), r.requested_at, r.resolved_at, oi.product_name
		FROM return_requests r
		JOIN order_items oi ON oi.id = r.order_item_id
		WHERE ` + where + ` ORDER BY r.requested_at DESC`

	var (
		rows pgx.Rows
		err  error
	)
	if len(args) == 0 {
		rows, err = r.pool.Query(ctx, query)
	} else {
		rows, err = r.pool.Query(ctx, query, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.ReturnRequest{}
	for rows.Next() {
		req, err := scanReturn(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, req)
	}
	return items, rows.Err()
}

// SetReturnStatus transitions a return request.
func (r *StoreRepository) SetReturnStatus(ctx context.Context, returnID, status, resolution, note, noteField string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE return_requests SET status = $2::varchar, resolution = NULLIF($3, ''), `+noteField+` = NULLIF($4, ''),
			resolved_at = CASE WHEN $2::varchar IN ('refunded','rejected','closed') THEN now() ELSE resolved_at END,
			updated_at = now()
		WHERE id = $1`, returnID, status, resolution, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *StoreRepository) returnBy(ctx context.Context, where string, arg any) (*domain.ReturnRequest, error) {
	var req domain.ReturnRequest
	var evidence []byte
	err := r.pool.QueryRow(ctx, `
		SELECT r.id, r.order_id, r.order_item_id, r.buyer_id, r.seller_id, r.reason, r.description,
		       r.evidence_urls, r.status, COALESCE(r.resolution,''), COALESCE(r.seller_note,''),
		       COALESCE(r.admin_note,''), r.requested_at, r.resolved_at, oi.product_name
		FROM return_requests r
		JOIN order_items oi ON oi.id = r.order_item_id
		`+where, arg).
		Scan(&req.ID, &req.OrderID, &req.OrderItemID, &req.BuyerID, &req.SellerID, &req.Reason,
			&req.Description, &evidence, &req.Status, &req.Resolution, &req.SellerNote,
			&req.AdminNote, &req.RequestedAt, &req.ResolvedAt, &req.ItemName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &req.EvidenceURLs)
	}
	return &req, nil
}

func (r *StoreRepository) loadReturnAmount(ctx context.Context, req *domain.ReturnRequest) (*domain.ReturnRequest, error) {
	if err := r.pool.QueryRow(ctx,
		`SELECT total FROM order_items WHERE id = $1`, req.OrderItemID).Scan(&req.Amount); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return req, nil
}

type returnRow interface {
	Scan(dest ...any) error
}

func scanReturn(row returnRow) (*domain.ReturnRequest, error) {
	var req domain.ReturnRequest
	var evidence []byte
	err := row.Scan(&req.ID, &req.OrderID, &req.OrderItemID, &req.BuyerID, &req.SellerID, &req.Reason,
		&req.Description, &evidence, &req.Status, &req.Resolution, &req.SellerNote,
		&req.AdminNote, &req.RequestedAt, &req.ResolvedAt, &req.ItemName)
	if err != nil {
		return nil, err
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &req.EvidenceURLs)
	}
	return &req, nil
}

var _ = time.Second

// ReturnAddress is where a seller's returns go.
//
// `stores` had NO address column until 00051 -- 00006_marketplace.sql:1-16 created it
// with name, slug, description, logo, banner, status and counters, and no location at
// all. So a return label had nowhere correct to be sent, and the two available
// fallbacks are both wrong: reusing `orders.shipping_address` posts the returned goods
// back to the BUYER at the seller's expense, and inventing one from the store's city
// is an address nobody chose.
//
// An empty address is returned as a nil map rather than a guessed one, so the caller
// cannot mistake "unset" for "set to something plausible".
func (r *StoreRepository) ReturnAddress(ctx context.Context, q Querier, ownerID string) (map[string]any, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT return_address FROM stores WHERE owner_id = $1::uuid`, ownerID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// A malformed address is treated as absent rather than propagated: a
		// half-readable address that gets partially filled in is how a parcel ends up
		// addressed to a street that does not exist.
		return nil, nil
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// SetReturnAddress records where a seller's returns go.
//
// A seller may only set their OWN store's address, which is the whole of the
// ownership check: there is no admin path here on purpose, because an admin editing a
// pickup address is how returns start arriving somewhere the seller cannot collect
// them.
func (r *StoreRepository) SetReturnAddress(
	ctx context.Context, ownerID string, addr map[string]any,
) error {
	raw, err := json.Marshal(addr)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE stores SET return_address = $2::jsonb, updated_at = now()
		 WHERE owner_id = $1::uuid`, ownerID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindNotFound, "STORE_NOT_FOUND",
			"no store is registered for this account, so a return address cannot be set")
	}
	return nil
}

// ReturnFacts reads the columns a return parcel needs, in one query.
//
// Deliberately NOT reusing `returnBy`, which joins `order_items` and scans an
// evidence array and a product name that none of this needs. A parcel path that pulls
// the whole return row is a parcel path that breaks when the return row grows a
// column.
func (r *StoreRepository) ReturnFacts(
	ctx context.Context, q Querier, returnID string,
) (orderID, itemID, buyerID, sellerID, status string, err error) {
	err = q.QueryRow(ctx, `
		SELECT order_id::text, order_item_id::text, buyer_id::text, seller_id::text, status
		  FROM return_requests WHERE id = $1::uuid`, returnID).
		Scan(&orderID, &itemID, &buyerID, &sellerID, &status)
	if err != nil {
		return "", "", "", "", "", err
	}
	return orderID, itemID, buyerID, sellerID, status, nil
}
