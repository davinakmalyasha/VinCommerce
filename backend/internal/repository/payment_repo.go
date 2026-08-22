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

// PaymentRepository persists payment intents, wallets and payouts.
type PaymentRepository struct {
	pool *db.Pool
}

// NewPaymentRepository creates a PaymentRepository.
func NewPaymentRepository(pool *db.Pool) *PaymentRepository {
	return &PaymentRepository{pool: pool}
}

// CreateIntent inserts a payment intent (idempotent by key).
func (r *PaymentRepository) CreateIntent(ctx context.Context, in *domain.PaymentIntent) error {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO payment_intents (id, order_id, buyer_id, amount, currency, status, gateway, gateway_ref, method, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.ID, in.OrderID, in.BuyerID, in.Amount, in.Currency, in.Status, in.Gateway, in.GatewayRef, in.Method, in.IdempotencyKey)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return domain.E(domain.KindConflict, "INTENT_EXISTS", "this order already has a payment intent")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "IDEMPOTENCY_REPLAY", "intent already exists for this idempotency key")
	}
	return nil
}

// IntentByOrder fetches the intent for an order.
func (r *PaymentRepository) IntentByOrder(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	var in domain.PaymentIntent
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_id, buyer_id, amount, currency, status, gateway,
		       COALESCE(gateway_ref,''), COALESCE(method,''), idempotency_key,
		       escrow_released_at, captured_at, refunded_at, fee_amount, seller_amount, created_at, updated_at
		FROM payment_intents WHERE order_id = $1`, orderID).
		Scan(&in.ID, &in.OrderID, &in.BuyerID, &in.Amount, &in.Currency, &in.Status, &in.Gateway,
			&in.GatewayRef, &in.Method, &in.IdempotencyKey,
			&in.EscrowReleasedAt, &in.CapturedAt, &in.RefundedAt, &in.FeeAmount, &in.SellerAmount,
			&in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_INTENT", "no payment intent for order")
	}
	return &in, err
}

// IntentByRef fetches an intent by gateway reference.
func (r *PaymentRepository) IntentByRef(ctx context.Context, gateway, ref string) (*domain.PaymentIntent, error) {
	var in domain.PaymentIntent
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_id, buyer_id, amount, currency, status, gateway,
		       COALESCE(gateway_ref,''), COALESCE(method,''), idempotency_key,
		       escrow_released_at, captured_at, refunded_at, created_at, updated_at
		FROM payment_intents WHERE gateway = $1 AND gateway_ref = $2`, gateway, ref).
		Scan(&in.ID, &in.OrderID, &in.BuyerID, &in.Amount, &in.Currency, &in.Status, &in.Gateway,
			&in.GatewayRef, &in.Method, &in.IdempotencyKey,
			&in.EscrowReleasedAt, &in.CapturedAt, &in.RefundedAt, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_INTENT", "no payment intent for reference")
	}
	return &in, err
}

// SetIntentStatus transitions the intent status.
func (r *PaymentRepository) SetIntentStatus(ctx context.Context, intentID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE payment_intents SET status = $2::varchar, updated_at = now(),
			captured_at = CASE WHEN $2::varchar = 'captured' THEN now() ELSE captured_at END,
			escrow_released_at = CASE WHEN $2::varchar = 'released' THEN now() ELSE escrow_released_at END,
			refunded_at = CASE WHEN $2::varchar = 'refunded' THEN now() ELSE refunded_at END
		WHERE id = $1`, intentID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Wallet fetches a wallet, creating it if absent.
func (r *PaymentRepository) Wallet(ctx context.Context, userID string) (*domain.Wallet, error) {
	var w domain.Wallet
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wallets (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
		RETURNING user_id, balance, held_balance, created_at, updated_at`, userID).
		Scan(&w.UserID, &w.Balance, &w.HeldBalance, &w.CreatedAt, &w.UpdatedAt)
	return &w, err
}

// WalletTx applies a ledger entry, updating balance atomically.
func (r *PaymentRepository) WalletTx(ctx context.Context, userID, kind, reason string, amount float64, refID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return err
	}
	var balanceAfter float64
	err = tx.QueryRow(ctx, `
		UPDATE wallets SET balance = balance + CASE WHEN $2::varchar = 'debit' THEN -$3 ELSE $3 END, updated_at = now()
		WHERE user_id = $1 AND ($2::varchar <> 'debit' OR balance >= $3)
		RETURNING balance`, userID, kind, amount).Scan(&balanceAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.E(domain.KindConflict, "INSUFFICIENT_BALANCE", "insufficient wallet balance")
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wallet_transactions (wallet_id, kind, reason, amount, balance_after, ref_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`,
		userID, kind, reason, amount, balanceAfter, refID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Transactions lists a wallet's ledger.
func (r *PaymentRepository) Transactions(ctx context.Context, walletID string, limit int) ([]*domain.WalletTransaction, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, wallet_id, kind, reason, amount, balance_after, COALESCE(ref_id,''), created_at
		FROM wallet_transactions WHERE wallet_id = $1 ORDER BY created_at DESC LIMIT $2`,
		walletID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	txs := []*domain.WalletTransaction{}
	for rows.Next() {
		var t domain.WalletTransaction
		if err := rows.Scan(&t.ID, &t.WalletID, &t.Kind, &t.Reason, &t.Amount, &t.BalanceAfter, &t.RefID, &t.CreatedAt); err != nil {
			return nil, err
		}
		txs = append(txs, &t)
	}
	return txs, rows.Err()
}

// CreatePayout requests a withdrawal.
func (r *PaymentRepository) CreatePayout(ctx context.Context, p *domain.Payout) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payouts (id, wallet_id, amount, status, bank_name, bank_account)
		VALUES ($1, $2, $3, 'pending', NULLIF($4, ''), NULLIF($5, ''))`,
		p.ID, p.WalletID, p.Amount, p.BankName, p.BankAccount)
	return err
}

// Payouts lists payouts for a wallet.
func (r *PaymentRepository) Payouts(ctx context.Context, walletID string) ([]*domain.Payout, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, wallet_id, amount, status, COALESCE(gateway_ref,''),
		       COALESCE(bank_name,''), COALESCE(bank_account,''), requested_at, processed_at
		FROM payouts WHERE wallet_id = $1 ORDER BY requested_at DESC`, walletID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	payouts := []*domain.Payout{}
	for rows.Next() {
		var p domain.Payout
		if err := rows.Scan(&p.ID, &p.WalletID, &p.Amount, &p.Status, &p.GatewayRef,
			&p.BankName, &p.BankAccount, &p.RequestedAt, &p.ProcessedAt); err != nil {
			return nil, err
		}
		payouts = append(payouts, &p)
	}
	return payouts, rows.Err()
}

// MarkPayoutSent marks a payout processed (sandbox simulates transfer).
func (r *PaymentRepository) MarkPayoutSent(ctx context.Context, payoutID, ref string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE payouts SET status = 'sent', gateway_ref = $2, processed_at = now()
		WHERE id = $1 AND status = 'pending'`, payoutID, ref)
	return err
}

// PlatformFee is the active commission configuration.
type PlatformFee struct {
	ID        string    `json:"id"`
	Pct       float64   `json:"pct"`
	Fixed     float64   `json:"fixed"`
	IsActive  bool      `json:"is_active"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ActiveFee returns the active commission config.
func (r *PaymentRepository) ActiveFee(ctx context.Context) (*PlatformFee, error) {
	var f PlatformFee
	err := r.pool.QueryRow(ctx, `
		SELECT id, pct, fixed, is_active, updated_at FROM platform_fees WHERE is_active ORDER BY updated_at DESC LIMIT 1`).
		Scan(&f.ID, &f.Pct, &f.Fixed, &f.IsActive, &f.UpdatedAt)
	return &f, err
}

// SetFee updates the commission config (admin).
func (r *PaymentRepository) SetFee(ctx context.Context, pct, fixed float64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE platform_fees SET is_active = FALSE WHERE is_active = TRUE;
		INSERT INTO platform_fees (id, pct, fixed, is_active) VALUES (gen_random_uuid(), $1, $2, TRUE);`,
		pct, fixed)
	return err
}

// Commission splits an amount into platform fee and seller proceeds.
func Commission(pct, fixed, amount float64) (fee, seller float64) {
	fee = amount * pct / 100
	fee += fixed
	if fee > amount {
		fee = amount
	}
	return fee, amount - fee
}

// ApplyFee records the fee split on an intent.
func (r *PaymentRepository) ApplyFee(ctx context.Context, intentID string, fee, sellerAmount float64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE payment_intents SET fee_amount = $2, seller_amount = $3 WHERE id = $1`,
		intentID, fee, sellerAmount)
	return err
}
