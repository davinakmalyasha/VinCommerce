package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// LoyaltyRepository persists loyalty points and referral codes.
type LoyaltyRepository struct {
	pool *db.Pool
}

// NewLoyaltyRepository creates a LoyaltyRepository.
func NewLoyaltyRepository(pool *db.Pool) *LoyaltyRepository {
	return &LoyaltyRepository{pool: pool}
}

// Balance returns the user's points balance.
func (r *LoyaltyRepository) Balance(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(change), 0)::int FROM loyalty_ledger WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// Add points with a reason.
func (r *LoyaltyRepository) Add(ctx context.Context, userID string, change int, reason, refID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO loyalty_ledger (user_id, change, reason, ref_id) VALUES ($1, $2, $3, NULLIF($4, ''))`,
		userID, change, reason, refID)
	return err
}

// Spend atomically debits points only when the balance covers it.
// Returns ErrInsufficientPoints-style conflict when it does not.
func (r *LoyaltyRepository) Spend(ctx context.Context, userID string, points int, reason, refID string) error {
	if points <= 0 {
		return domain.E(domain.KindInvalid, "BAD_POINTS", "points must be positive")
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO loyalty_ledger (user_id, change, reason, ref_id)
		SELECT $1, $2, $3, NULLIF($4, '')
		WHERE (SELECT COALESCE(SUM(change), 0)::int FROM loyalty_ledger WHERE user_id = $1) >= $2`,
		userID, -points, reason, refID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INSUFFICIENT_POINTS", "not enough loyalty points")
	}
	return nil
}

// Ledger lists the user's point history.
func (r *LoyaltyRepository) Ledger(ctx context.Context, userID string, limit int) ([]*domain.LoyaltyEntry, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, change, reason, COALESCE(ref_id,''), created_at
		FROM loyalty_ledger WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.LoyaltyEntry{}
	for rows.Next() {
		var e domain.LoyaltyEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Change, &e.Reason, &e.RefID, &e.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &e)
	}
	return items, rows.Err()
}

// ReferralCode returns a user's code, creating one if absent.
func (r *LoyaltyRepository) ReferralCode(ctx context.Context, userID string) (string, error) {
	var code string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO referral_codes (user_id, code)
		VALUES ($1, upper(substr(md5(random()::text), 1, 8)))
		ON CONFLICT (user_id) DO NOTHING
		RETURNING (SELECT code FROM referral_codes WHERE user_id = $1)`, userID).Scan(&code)
	if err != nil {
		err = r.pool.QueryRow(ctx,
			`SELECT code FROM referral_codes WHERE user_id = $1`, userID).Scan(&code)
	}
	return code, err
}

// ReferralByCode resolves a code to its owner (for registration rewards).
func (r *LoyaltyRepository) ReferralByCode(ctx context.Context, code string) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx,
		`SELECT user_id FROM referral_codes WHERE upper(code) = upper($1)`, code).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.E(domain.KindNotFound, "REFERRAL_NOT_FOUND", "referral code not found")
	}
	return userID, err
}

// DisputeRepository persists escalated disputes.
type DisputeRepository struct {
	pool *db.Pool
}

// NewDisputeRepository creates a DisputeRepository.
func NewDisputeRepository(pool *db.Pool) *DisputeRepository {
	return &DisputeRepository{pool: pool}
}

// Create opens a dispute.
func (r *DisputeRepository) Create(ctx context.Context, d *domain.Dispute) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO disputes (id, order_id, return_id, user_id, seller_id, subject, description)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7) RETURNING created_at`,
		d.ID, d.OrderID, d.ReturnID, d.UserID, d.SellerID, d.Subject, d.Description).Scan(&d.CreatedAt)
	return err
}

// DisputeByID fetches one dispute.
func (r *DisputeRepository) DisputeByID(ctx context.Context, disputeID string) (*domain.Dispute, error) {
	var d domain.Dispute
	err := r.pool.QueryRow(ctx, `
		SELECT d.id, d.order_id, d.return_id, d.user_id, d.seller_id, d.subject, d.description,
		       d.status, COALESCE(d.decision,''), COALESCE(d.admin_note,''), d.created_at, d.resolved_at
		FROM disputes d
		WHERE d.id = $1`, disputeID).
		Scan(&d.ID, &d.OrderID, &d.ReturnID, &d.UserID, &d.SellerID, &d.Subject, &d.Description,
			&d.Status, &d.Decision, &d.AdminNote, &d.CreatedAt, &d.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListByStatus lists disputes (admin).
func (r *DisputeRepository) ListByStatus(ctx context.Context, status string) ([]*domain.Dispute, error) {
	where := `d.status <> 'closed'`
	args := []any{}
	if status != "" && status != "all" {
		where = `d.status = $1`
		args = append(args, status)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.order_id, d.return_id, d.user_id, d.seller_id, d.subject, d.description,
		       d.status, COALESCE(d.decision,''), COALESCE(d.admin_note,''), d.created_at, d.resolved_at,
		       b.full_name, s.name
		FROM disputes d
		JOIN users b ON b.id = d.user_id
		LEFT JOIN stores s ON s.owner_id = d.seller_id
		WHERE `+where+` ORDER BY d.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Dispute{}
	for rows.Next() {
		var d domain.Dispute
		if err := rows.Scan(&d.ID, &d.OrderID, &d.ReturnID, &d.UserID, &d.SellerID, &d.Subject, &d.Description,
			&d.Status, &d.Decision, &d.AdminNote, &d.CreatedAt, &d.ResolvedAt, &d.BuyerName, &d.SellerName); err != nil {
			return nil, err
		}
		items = append(items, &d)
	}
	return items, rows.Err()
}

// Resolve decides a dispute (admin).
func (r *DisputeRepository) Resolve(ctx context.Context, disputeID, decision, note string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE disputes SET status = 'resolved', decision = $2, admin_note = NULLIF($3, ''), resolved_at = now()
		WHERE id = $1`, disputeID, decision, note)
	return err
}
