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
		INSERT INTO payment_intents (id, order_id, buyer_id, amount, currency, status, gateway,
			gateway_ref, snap_token, gateway_txn_id, redirect_url, method, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,''), NULLIF($10,''), NULLIF($11,''),
			NULLIF($12, ''), $13)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.ID, in.OrderID, in.BuyerID, in.Amount, in.Currency, in.Status, in.Gateway,
		in.GatewayRef, in.SnapToken, in.GatewayTxnID, in.RedirectURL, in.Method, in.IdempotencyKey)
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

// CreateIntentTx inserts a payment intent inside a caller-managed transaction.
func (r *PaymentRepository) CreateIntentTx(ctx context.Context, tx pgx.Tx, in *domain.PaymentIntent) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO payment_intents (id, order_id, buyer_id, amount, currency, status, gateway,
			gateway_ref, snap_token, gateway_txn_id, redirect_url, method, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,''), NULLIF($10,''), NULLIF($11,''),
			NULLIF($12, ''), $13)`,
		in.ID, in.OrderID, in.BuyerID, in.Amount, in.Currency, in.Status, in.Gateway,
		in.GatewayRef, in.SnapToken, in.GatewayTxnID, in.RedirectURL, in.Method, in.IdempotencyKey)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return domain.E(domain.KindConflict, "INTENT_EXISTS", "this order already has a payment intent")
		}
	}
	return err
}

// intentColumns are the shared SELECT columns for intent lookups.
const intentColumns = `
	id, order_id, buyer_id, amount, currency, status, gateway,
	COALESCE(gateway_ref,''), COALESCE(snap_token,''), COALESCE(gateway_txn_id,''), COALESCE(redirect_url,''),
	COALESCE(method,''), idempotency_key,
	escrow_released_at, captured_at, refunded_at, fee_amount, seller_amount, created_at, updated_at`

func scanIntent(row pgx.Row) (*domain.PaymentIntent, error) {
	var in domain.PaymentIntent
	err := row.Scan(&in.ID, &in.OrderID, &in.BuyerID, &in.Amount, &in.Currency, &in.Status, &in.Gateway,
		&in.GatewayRef, &in.SnapToken, &in.GatewayTxnID, &in.RedirectURL,
		&in.Method, &in.IdempotencyKey,
		&in.EscrowReleasedAt, &in.CapturedAt, &in.RefundedAt, &in.FeeAmount, &in.SellerAmount,
		&in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// IntentByOrder fetches the intent for an order.
func (r *PaymentRepository) IntentByOrder(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	in, err := scanIntent(r.pool.QueryRow(ctx,
		`SELECT `+intentColumns+` FROM payment_intents WHERE order_id = $1`, orderID))
	if domain.Is(err, domain.KindNotFound, "") {
		return nil, domain.E(domain.KindNotFound, "NO_INTENT", "no payment intent for order")
	}
	return in, err
}

// IntentByRef fetches an intent by gateway reference.
func (r *PaymentRepository) IntentByRef(ctx context.Context, gateway, ref string) (*domain.PaymentIntent, error) {
	in, err := scanIntent(r.pool.QueryRow(ctx,
		`SELECT `+intentColumns+` FROM payment_intents WHERE gateway = $1 AND gateway_ref = $2`, gateway, ref))
	if domain.Is(err, domain.KindNotFound, "") {
		return nil, domain.E(domain.KindNotFound, "NO_INTENT", "no payment intent for reference")
	}
	return in, err
}

// IntentByKey fetches an intent by its idempotency key.
func (r *PaymentRepository) IntentByKey(ctx context.Context, key string) (*domain.PaymentIntent, error) {
	in, err := scanIntent(r.pool.QueryRow(ctx,
		`SELECT `+intentColumns+` FROM payment_intents WHERE idempotency_key = $1`, key))
	if domain.Is(err, domain.KindNotFound, "") {
		return nil, domain.E(domain.KindNotFound, "NO_INTENT", "no payment intent for key")
	}
	return in, err
}

// SetIntentStatus transitions the intent status.
func (r *PaymentRepository) SetIntentStatus(ctx context.Context, intentID, status string) error {
	return setIntentStatusOn(r.pool, ctx, intentID, status)
}

// SetIntentStatusTx transitions the intent status inside the given transaction.
func (r *PaymentRepository) SetIntentStatusTx(ctx context.Context, tx pgx.Tx, intentID, status string) error {
	return setIntentStatusOn(tx, ctx, intentID, status)
}

// SetIntentStatusGuarded transitions the intent only when its current status is
// one of the expected values; returns a conflict error otherwise. This makes
// financial state machines idempotent and replay-safe.
func (r *PaymentRepository) SetIntentStatusGuarded(ctx context.Context, intentID string, expected []string, to string) error {
	return setIntentStatusGuardedOn(r.pool, ctx, intentID, expected, to)
}

// SetIntentStatusGuardedTx is SetIntentStatusGuarded inside a transaction.
func (r *PaymentRepository) SetIntentStatusGuardedTx(ctx context.Context, tx pgx.Tx, intentID string, expected []string, to string) error {
	return setIntentStatusGuardedOn(tx, ctx, intentID, expected, to)
}

// SumRefundedByOrder totals the refund credits already posted for an order, so
// a second partial refund can be bounded by what is actually left.
//
// Derived from the ledger rather than from a column on payment_intents,
// because a `partially_refunded` intent does not record how much has already
// gone back, and the ledger is the only record guaranteed to agree with the
// money that actually moved.
func (r *PaymentRepository) SumRefundedByOrder(ctx context.Context, orderID string, out *float64) error {
	var total float64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)::float8
		FROM wallet_transactions
		WHERE ref_id = $1 AND reason = $2 AND kind = 'credit'`,
		orderID, domain.TxReasonRefund).Scan(&total)
	if err != nil {
		return err
	}
	*out = total
	return nil
}

// HasEscrowRelease reports whether escrow was ever paid out for an order. A
// partially_refunded intent no longer records that, so it is read from the
// ledger row the release wrote.
// HasEscrowRelease reports whether this order's escrow was ever paid out.
//
// It takes a Querier rather than a pgx.Tx so the payment service can call it
// with its own transaction handle without naming the pgx type. That is the
// only reason this signature is not just pgx.Tx: every other "-Tx" method in
// this file is called through the OrderTx unit of work, and having one method
// that required the raw handle is what pushed pgx into the service layer's
// import list.
func (r *PaymentRepository) HasEscrowRelease(ctx context.Context, q Querier, orderID string, out *bool) error {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM wallet_transactions
			WHERE ref_id = $1 AND reason = $2 AND kind = 'credit'
		)`, orderID, domain.TxReasonEscrowRelease).Scan(&exists)
	if err != nil {
		return err
	}
	*out = exists
	return nil
}

func intentStatusUpdate(expected []string) string {
	q := `
		UPDATE payment_intents SET status = $3::varchar, updated_at = now(),
			captured_at = CASE WHEN $3::varchar = 'captured' THEN now() ELSE captured_at END,
			escrow_released_at = CASE WHEN $3::varchar = 'released' THEN now() ELSE escrow_released_at END,
			refunded_at = CASE WHEN $3::varchar = 'refunded' THEN now() ELSE refunded_at END
		WHERE id = $1`
	if len(expected) > 0 {
		q += ` AND status = ANY($2::varchar[])`
	}
	return q
}

func intentStatusArgs(intentID string, expected []string, status string) []any {
	args := []any{intentID}
	if len(expected) > 0 {
		args = append(args, expected)
	}
	return append(args, status)
}

func setIntentStatusOn(q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}, ctx context.Context, intentID, status string) error {
	tag, err := q.Exec(ctx, intentStatusUpdate(nil), intentStatusArgs(intentID, nil, status)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func setIntentStatusGuardedOn(q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}, ctx context.Context, intentID string, expected []string, to string) error {
	tag, err := q.Exec(ctx, intentStatusUpdate(expected), intentStatusArgs(intentID, expected, to)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INTENT_STATE",
			"payment intent is not in an expected state for this transition")
	}
	return nil
}

// SetIntentMethod records the concrete channel used at the gateway
// (e.g. Midtrans payment_type: gopay, qris, bank_transfer, kredivo).
func (r *PaymentRepository) SetIntentMethod(ctx context.Context, intentID, method string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE payment_intents SET method = $2, updated_at = now() WHERE id = $1`, intentID, method)
	return err
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
	if err := r.WalletTxOn(ctx, tx, userID, kind, reason, amount, refID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WalletTxOn applies a ledger entry inside a caller-managed transaction so it
// can be composed atomically with intent status changes and payouts.
func (r *PaymentRepository) WalletTxOn(ctx context.Context, tx pgx.Tx, userID, kind, reason string, amount float64, refID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return err
	}
	var balanceAfter float64
	err := tx.QueryRow(ctx, `
		UPDATE wallets SET balance = balance + CASE WHEN $2::varchar = 'debit' THEN -$3 ELSE $3 END, updated_at = now()
		WHERE user_id = $1 AND ($2::varchar <> 'debit' OR balance >= $3)
		RETURNING balance`, userID, kind, amount).Scan(&balanceAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.E(domain.KindConflict, "INSUFFICIENT_BALANCE", "insufficient wallet balance")
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_transactions (wallet_id, kind, reason, amount, balance_after, ref_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`,
		userID, kind, reason, amount, balanceAfter, refID)
	return err
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

// CreatePayoutTx inserts a payout inside a caller-managed transaction.
func (r *PaymentRepository) CreatePayoutTx(ctx context.Context, tx pgx.Tx, p *domain.Payout) error {
	_, err := tx.Exec(ctx, `
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

// AdminPayout is a withdrawal request joined with the requester identity.
type AdminPayout struct {
	ID          string     `json:"id"`
	WalletID    string     `json:"wallet_id"`
	UserEmail   string     `json:"user_email"`
	UserName    string     `json:"user_name"`
	Amount      float64    `json:"amount"`
	Status      string     `json:"status"`
	GatewayRef  string     `json:"gateway_ref"`
	BankName    string     `json:"bank_name"`
	BankAccount string     `json:"bank_account"`
	RequestedAt time.Time  `json:"requested_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// PayoutsByStatus lists withdrawal requests for the admin queue.
func (r *PaymentRepository) PayoutsByStatus(ctx context.Context, status string, limit int) ([]*AdminPayout, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if status == "" {
		status = "pending"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.wallet_id, u.email, u.full_name, p.amount, p.status,
		       COALESCE(p.gateway_ref,''), COALESCE(p.bank_name,''), COALESCE(p.bank_account,''),
		       p.requested_at, p.processed_at
		FROM payouts p
		JOIN users u ON u.id = p.wallet_id
		WHERE p.status = $1
		ORDER BY p.requested_at
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*AdminPayout{}
	for rows.Next() {
		var p AdminPayout
		if err := rows.Scan(&p.ID, &p.WalletID, &p.UserEmail, &p.UserName, &p.Amount,
			&p.Status, &p.GatewayRef, &p.BankName, &p.BankAccount,
			&p.RequestedAt, &p.ProcessedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// CompletePayout marks a payout sent (admin confirms real transfer happened).
func (r *PaymentRepository) CompletePayout(ctx context.Context, payoutID, ref string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE payouts SET status = 'sent', gateway_ref = NULLIF($2, ''), processed_at = now()
		WHERE id = $1 AND status = 'pending'`, payoutID, ref)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "NOT_PENDING", "payout is not awaiting processing")
	}
	return nil
}

// FailPayout rejects a pending payout and refunds the amount to the seller's
// wallet — atomically, so funds can never be stuck debited without a record.
func (r *PaymentRepository) FailPayout(ctx context.Context, payoutID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var (
		walletID string
		amount   float64
	)
	err = tx.QueryRow(ctx,
		`SELECT wallet_id, amount FROM payouts WHERE id = $1 AND status = 'pending' FOR UPDATE`,
		payoutID).Scan(&walletID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.E(domain.KindConflict, "NOT_PENDING", "payout is not awaiting processing")
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payouts SET status = 'failed', processed_at = now() WHERE id = $1`, payoutID); err != nil {
		return err
	}
	// Refund the reservation debit; 'adjustment' is the ledger reason the
	// wallet UI already labels.
	if err := r.WalletTxOn(ctx, tx, walletID, "credit", "adjustment", amount, payoutID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// ActiveFeeTx returns the active commission config inside a transaction.
func (r *PaymentRepository) ActiveFeeTx(ctx context.Context, tx pgx.Tx) (*PlatformFee, error) {
	var f PlatformFee
	err := tx.QueryRow(ctx, `
		SELECT id, pct, fixed, is_active, updated_at FROM platform_fees WHERE is_active ORDER BY updated_at DESC LIMIT 1`).
		Scan(&f.ID, &f.Pct, &f.Fixed, &f.IsActive, &f.UpdatedAt)
	return &f, err
}

// ApplyFeeTx records the fee split inside a caller-managed transaction.
func (r *PaymentRepository) ApplyFeeTx(ctx context.Context, tx pgx.Tx, intentID string, fee, sellerAmount float64) error {
	_, err := tx.Exec(ctx, `
		UPDATE payment_intents SET fee_amount = $2, seller_amount = $3 WHERE id = $1`,
		intentID, fee, sellerAmount)
	return err
}
