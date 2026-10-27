package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// ExternalPaymentClaim is a buyer's assertion that an off-platform transfer was made.
// It is not money and it is not a receipt: an admin decides whether it is true.
type ExternalPaymentClaim struct {
	ID         string     `json:"id"`
	OrderID    string     `json:"order_id"`
	BuyerID    string     `json:"buyer_id"`
	Reference  string     `json:"reference"`
	Amount     float64    `json:"amount"`
	PaidAt     time.Time  `json:"paid_at"`
	Status     string     `json:"status"`
	ReviewedBy *string    `json:"reviewed_by,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	Note       string     `json:"note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`

	// Display fields for the admin queue, joined from the order and the buyer.
	OrderNumber string  `json:"order_number"`
	OrderTotal  float64 `json:"order_total"`
	BuyerEmail  string  `json:"buyer_email"`
}

// PaymentClaimRepository stores off-platform payment claims.
type PaymentClaimRepository struct {
	pool *db.Pool
}

// NewPaymentClaimRepository builds the repository.
func NewPaymentClaimRepository(pool *db.Pool) *PaymentClaimRepository {
	return &PaymentClaimRepository{pool: pool}
}

// Create records a pending claim. The partial unique index makes a second open claim
// for the same order a conflict rather than a second pending row.
func (r *PaymentClaimRepository) Create(ctx context.Context, orderID, buyerID, reference string, amount float64, paidAt time.Time) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO external_payment_claims (order_id, buyer_id, reference, amount, paid_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, orderID, buyerID, reference, amount, paidAt).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", domain.E(domain.KindConflict, "CLAIM_PENDING",
				"a payment claim for this order is already awaiting verification")
		}
		return "", err
	}
	return id, nil
}

// ByID returns one claim with the display fields the admin queue needs.
func (r *PaymentClaimRepository) ByID(ctx context.Context, id string) (*ExternalPaymentClaim, error) {
	c, err := scanClaim(r.pool.QueryRow(ctx, `
		SELECT `+claimColumns+`
		FROM external_payment_claims c
		JOIN orders o ON o.id = c.order_id
		JOIN users u ON u.id = c.buyer_id
		WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_CLAIM", "payment claim not found")
	}
	return c, err
}

// Pending lists claims awaiting a decision, oldest first.
func (r *PaymentClaimRepository) Pending(ctx context.Context) ([]*ExternalPaymentClaim, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+claimColumns+`
		FROM external_payment_claims c
		JOIN orders o ON o.id = c.order_id
		JOIN users u ON u.id = c.buyer_id
		WHERE c.status = 'pending'
		ORDER BY c.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ExternalPaymentClaim{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Decide records the admin's decision. It only ever moves a claim out of 'pending',
// so two admins deciding at once cannot both succeed.
func (r *PaymentClaimRepository) Decide(ctx context.Context, id, status, adminID, note string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE external_payment_claims
		SET status = $2, reviewed_by = $3, reviewed_at = now(), note = $4
		WHERE id = $1 AND status = 'pending'`, id, status, adminID, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "CLAIM_DECIDED",
			"this claim has already been reviewed")
	}
	return nil
}

const claimColumns = `c.id, c.order_id, c.buyer_id, c.reference, c.amount, c.paid_at,
	       c.status, c.reviewed_by, c.reviewed_at, c.note, c.created_at,
	       o.order_number, o.total_amount, u.email`

type claimScanner interface {
	Scan(dest ...any) error
}

func scanClaim(s claimScanner) (*ExternalPaymentClaim, error) {
	var c ExternalPaymentClaim
	err := s.Scan(&c.ID, &c.OrderID, &c.BuyerID, &c.Reference, &c.Amount, &c.PaidAt,
		&c.Status, &c.ReviewedBy, &c.ReviewedAt, &c.Note, &c.CreatedAt,
		&c.OrderNumber, &c.OrderTotal, &c.BuyerEmail)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
