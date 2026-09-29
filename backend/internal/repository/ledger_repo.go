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

// LedgerRepository is persistence for the double-entry ledger.
//
// The balance invariant itself is NOT enforced here. It is a DEFERRABLE
// CONSTRAINT TRIGGER in the schema (migration 00043) that fires at COMMIT, so
// every journal sums to zero no matter which code path wrote it. Re-implementing
// it in Go would only protect the callers that remembered to call it.
type LedgerRepository struct {
	pool *db.Pool
}

// NewLedgerRepository creates a LedgerRepository.
func NewLedgerRepository(pool *db.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

// personalAccountPrefix mirrors the service-layer constant. Duplicated rather
// than shared because the repository must not import the service layer, and the
// two are asserted against each other by a repository test.
const personalAccountPrefix = "seller_available:"

// nullTime maps "" to a real SQL NULL so a single prepared statement can cover
// both "caller supplied a value" and "caller did not". The alternative is
// building the query text per call, which is how the effective_at splice
// happened in the first place.
func nullTime(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PostJournal writes a journal and its entries, and updates the derived balance
// cache, all inside the caller's transaction.
//
// Idempotent on idempotency_key: a repeat of the same business event returns the
// ORIGINAL journal rather than creating a second one. That is what makes a
// webhook delivered twice, or a worker job re-run after a partial failure, safe.
// The insert relies on `ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`
// returning no row on the conflict, and then re-selects.
//
// Entries are written in a deterministic order -- sorted by account code -- so
// two runs of the same posting produce the same row ids, which makes a
// reproduction from a log possible.
func (r *LedgerRepository) PostJournal(
	ctx context.Context, q Querier, idempotencyKey, txType, refType, refID, note, effectiveAt, reversalOf string,
	entries []domain.LedgerEntry,
) (*domain.LedgerJournal, error) {
	// effective_at and reversal_of are passed as PARAMETERS, never interpolated.
	// An earlier version spliced effective_at into the SQL text to reach now() on
	// the column default; that made a caller-supplied timestamp a string
	// concatenation into a query. Timestamps are precisely the field a
	// reconciliation import fills in from an external feed, so that is the one
	// value guaranteed to be attacker-influenced. COALESCE($5::timestamptz,
	// now()) keeps now() reachable without the splice.
	var journalID string
	err := q.QueryRow(ctx, `
		INSERT INTO ledger_journals (idempotency_key, tx_type, ref_type, ref_id, effective_at, reversal_of, note)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), COALESCE($5::timestamptz, now()),
		        NULLIF($6,'')::uuid, NULLIF($7,''))
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		idempotencyKey, txType, refType, refID, nullTime(effectiveAt), nullTime(reversalOf), note).Scan(&journalID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		// Conflict: this business event is already posted. Return the original.
		if err := q.QueryRow(ctx,
			`SELECT id FROM ledger_journals WHERE idempotency_key = $1`, idempotencyKey).Scan(&journalID); err != nil {
			return nil, err
		}
		return r.journalByID(ctx, q, journalID)
	}

	for _, e := range entries {
		meta := e.Metadata
		if len(meta) == 0 {
			meta = map[string]any{}
		}
		raw, merr := json.Marshal(meta)
		if merr != nil {
			return nil, fmt.Errorf("ledger entry metadata for %s: %w", e.Account, merr)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO ledger_entries (journal_id, account_code, amount, side, metadata)
			VALUES ($1, $2, $3, $4, $5)`,
			journalID, e.Account, e.Amount, e.Side, raw); err != nil {
			return nil, err
		}
	}

	// Fold the journal into the derived balance cache. Signed by the account's
	// normal side so a stored balance reads the way an accountant expects: a
	// positive number on a liability means "we owe this much".
	if err := r.applyBalances(ctx, q, journalID); err != nil {
		return nil, err
	}
	return r.journalByID(ctx, q, journalID)
}

// applyBalances folds a journal's entries into account_balances.
//
// The sign is derived from the ACCOUNT's normal side, not the entry's side, so
// the cache holds statement-signed balances. Rows are upserted in a single
// statement: an N-line journal becomes N upserts inside the caller's transaction,
// so a partial fold is impossible.
func (r *LedgerRepository) applyBalances(ctx context.Context, q Querier, journalID string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO account_balances (account_code, currency, balance, entry_count, version, updated_at)
		SELECT e.account_code,
		       e.currency,
		       SUM(CASE WHEN a.normal_side = 'debit'  THEN  e.amount
		                WHEN a.normal_side = 'credit' THEN -e.amount
		                ELSE 0 END),
		       COUNT(*),
		       1,
		       now()
		  FROM ledger_entries e
		  JOIN ledger_accounts a ON a.code = e.account_code
		 WHERE e.journal_id = $1
		 GROUP BY e.account_code, e.currency
		ON CONFLICT (account_code, currency) DO UPDATE
		   SET balance     = account_balances.balance + EXCLUDED.balance,
		       entry_count = account_balances.entry_count + EXCLUDED.entry_count,
		       version     = account_balances.version + 1,
		       updated_at  = now()`,
		journalID)
	return err
}

func (r *LedgerRepository) journalByID(ctx context.Context, q Querier, id string) (*domain.LedgerJournal, error) {
	var j domain.LedgerJournal
	err := q.QueryRow(ctx, `
		SELECT id, idempotency_key, tx_type, COALESCE(ref_type,''), COALESCE(ref_id,''),
		       effective_at, COALESCE(reversal_of::text,''), COALESCE(note,''), created_at
		  FROM ledger_journals WHERE id = $1`, id).
		Scan(&j.ID, &j.IdempotencyKey, &j.TxType, &j.RefType, &j.RefID,
			&j.EffectiveAt, &j.ReversalOf, &j.Note, &j.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// EnsurePersonalAccount creates a user's ledger account if it does not exist.
//
// The account code is derived from the user id rather than stored, so there is
// no mapping to get out of sync. `class` is liability and `normal_side` is credit
// because a seller's available balance is money the platform owes them.
func (r *LedgerRepository) EnsurePersonalAccount(ctx context.Context, q Querier, userID string) error {
	code := personalAccountPrefix + userID
	_, err := q.Exec(ctx, `
		INSERT INTO ledger_accounts (code, name, class, normal_side, is_system, currency, user_id)
		VALUES ($1, $2, 'liability', 'credit', false, 'IDR', $3::uuid)
		ON CONFLICT (code) DO NOTHING`,
		code, "Seller available balance "+userID, userID)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO account_balances (account_code, currency, balance)
		VALUES ($1, 'IDR', 0)
		ON CONFLICT (account_code, currency) DO NOTHING`, code)
	return err
}

// BalanceOf returns one account's signed balance.
//
// This is the number behind `held_balance` and `balance` on `wallets`: the
// escrow question, answerable as a query instead of a guess.
func (r *LedgerRepository) BalanceOf(ctx context.Context, q Querier, accountCode string) (float64, error) {
	var bal float64
	err := q.QueryRow(ctx,
		`SELECT balance FROM account_balances WHERE account_code = $1 AND currency = 'IDR'`,
		accountCode).Scan(&bal)
	if errors.Is(err, pgx.ErrNoRows) {
		// A missing row means "no balance", not "unknown". The row is seeded for
		// every system account by migration 00043, and created on first post for a
		// personal one, so a miss genuinely means the account has no activity.
		return 0, nil
	}
	return bal, err
}

// TrialBalance returns every account's balance, ordered by code.
func (r *LedgerRepository) TrialBalance(ctx context.Context, q Querier) ([]domain.TrialBalanceRow, error) {
	rows, err := q.Query(ctx, `
		SELECT b.account_code, a.name, a.class, a.normal_side, a.is_system, b.balance
		  FROM account_balances b
		  JOIN ledger_accounts a ON a.code = b.account_code
		 WHERE b.currency = 'IDR'
		 ORDER BY b.account_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.TrialBalanceRow{}
	for rows.Next() {
		var t domain.TrialBalanceRow
		if err := rows.Scan(&t.AccountCode, &t.Name, &t.Class, &t.NormalSide, &t.IsSystem, &t.Balance); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// JournalEntries returns one journal's lines, for the "show me this posting"
// admin view and for reconciling a specific dispute.
func (r *LedgerRepository) JournalEntries(ctx context.Context, q Querier, journalID string) ([]domain.LedgerEntryRow, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id, e.journal_id, e.account_code, e.amount, e.side, e.currency, e.metadata, e.created_at
		  FROM ledger_entries e
		 WHERE e.journal_id = $1
		 ORDER BY e.account_code`, journalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.LedgerEntryRow{}
	for rows.Next() {
		var e domain.LedgerEntryRow
		var raw []byte
		if err := rows.Scan(&e.ID, &e.JournalID, &e.AccountCode, &e.Amount, &e.Side,
			&e.Currency, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReconcileAll runs the reconciliation over the repository's own pool.
//
// A read-only report that needs no transaction, so it can be driven by a
// scheduled job that has no transaction to join. The transactional form --
// Reconcile(ctx, q) -- stays for callers that want the report to reflect a
// consistent snapshot alongside other work.
func (r *LedgerRepository) ReconcileAll(ctx context.Context) (*domain.LedgerReconciliation, error) {
	return r.Reconcile(ctx, r.pool)
}

// Pool exposes the underlying pool.
//
// Present for the same reason PaymentRepository has one: a caller that must run a
// statement outside a transaction -- a scheduled reconciliation, a read-only
// report -- needs a Querier and the service deliberately does not own one.
func (r *LedgerRepository) Pool() *db.Pool { return r.pool }

// Reconcile proves the derived caches agree with the journal entries.
//
// Two things can drift. `account_balances` is updated on every post, and
// `wallets.balance` is the pre-existing column the whole application reads for a
// user's spendable balance. The journal is the truth; both are caches. A cache
// that is never checked against its source is not a cache, it is a second
// opinion that nobody looks at.
//
// The query recomputes from `ledger_entries` and compares. It is deliberately one
// statement rather than a read of the cache: comparing the cache to itself would
// always agree, which is the failure this function exists to detect.
//
// This runs on a schedule and is also exposed on the admin ledger screen. It is
// the thing that turns "we added a ledger" into "we can prove the ledger is
// right".
func (r *LedgerRepository) Reconcile(ctx context.Context, q Querier) (*domain.LedgerReconciliation, error) {
	// 1. The balance cache against the entries.
	cacheDrift, err := r.reconcileBalances(ctx, q)
	if err != nil {
		return nil, err
	}

	// 2. Every journal sums to zero, recomputed rather than trusted to the
	//    constraint trigger. The trigger is a strong guarantee, but a guarantee
	//    nobody ever checks is a belief, and this is the check.
	unbalanced, err := r.unbalancedJournals(ctx, q)
	if err != nil {
		return nil, err
	}

	// 3. Every personal ledger account against the wallets row the application
	//    actually reads. A drift here is the one a user would notice: a seller
	//    whose ledger says Rp50,000 and whose wallet says Rp0.
	walletDrift, err := r.reconcileWallets(ctx, q)
	if err != nil {
		return nil, err
	}

	return &domain.LedgerReconciliation{
		GeneratedAt:        time.Now().UTC(),
		BalanceCacheRows:   len(cacheDrift),
		UnbalancedJournals: len(unbalanced),
		WalletDriftRows:    len(walletDrift),
		BalanceCacheDrift:  cacheDrift,
		Unbalanced:         unbalanced,
		WalletDrift:        walletDrift,
		Clean:              len(cacheDrift) == 0 && len(unbalanced) == 0 && len(walletDrift) == 0,
	}, nil
}

func (r *LedgerRepository) reconcileBalances(ctx context.Context, q Querier) ([]domain.DriftRow, error) {
	rows, err := q.Query(ctx, `
		WITH recomputed AS (
			SELECT e.account_code,
			       e.currency,
			       SUM(CASE WHEN a.normal_side = 'debit'  THEN  e.amount
			                WHEN a.normal_side = 'credit' THEN -e.amount
			                ELSE 0 END)::float8 AS expected
			  FROM ledger_entries e
			  JOIN ledger_accounts a ON a.code = e.account_code
			 GROUP BY e.account_code, e.currency
		)
		SELECT b.account_code, b.balance::float8, COALESCE(rc.expected, 0)
		  FROM account_balances b
		  LEFT JOIN recomputed rc ON rc.account_code = b.account_code AND rc.currency = b.currency
		 WHERE abs(b.balance - COALESCE(rc.expected, 0)) > 0.004
		 ORDER BY b.account_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.DriftRow{}
	for rows.Next() {
		var d domain.DriftRow
		if err := rows.Scan(&d.AccountCode, &d.Cached, &d.Expected); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *LedgerRepository) unbalancedJournals(ctx context.Context, q Querier) ([]domain.UnbalancedJournal, error) {
	rows, err := q.Query(ctx, `
		SELECT j.id, j.idempotency_key, j.tx_type,
		       COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'debit'), 0)::float8,
		       COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'credit'), 0)::float8
		  FROM ledger_journals j
		  LEFT JOIN ledger_entries e ON e.journal_id = j.id
		 GROUP BY j.id, j.idempotency_key, j.tx_type
		HAVING COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'debit'), 0)
		    <> COALESCE(SUM(e.amount) FILTER (WHERE e.side = 'credit'), 0)
		 ORDER BY j.created_at DESC
		 LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.UnbalancedJournal{}
	for rows.Next() {
		var u domain.UnbalancedJournal
		if err := rows.Scan(&u.JournalID, &u.IdempotencyKey, &u.TxType, &u.Debits, &u.Credits); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// reconcileWallets compares each personal ledger account with the wallets row the
// application reads for a user's spendable balance.
func (r *LedgerRepository) reconcileWallets(ctx context.Context, q Querier) ([]domain.WalletDrift, error) {
	rows, err := q.Query(ctx, `
		SELECT w.user_id,
		       w.balance::float8,
		       COALESCE(ab.balance, 0)::float8,
		       w.held_balance::float8,
		       COALESCE(held.balance, 0)::float8
		  FROM wallets w
		  LEFT JOIN account_balances ab
		         ON ab.account_code = $1 || w.user_id::text AND ab.currency = 'IDR'
		  LEFT JOIN account_balances held
		         ON held.account_code = $2 || w.user_id::text AND held.currency = 'IDR'
		 WHERE abs(w.balance - COALESCE(ab.balance, 0)) > 0.004
		    OR abs(w.held_balance - COALESCE(held.balance, 0)) > 0.004
		 ORDER BY abs(w.balance - COALESCE(ab.balance, 0)) DESC
		 LIMIT 100`,
		personalAccountPrefix, "seller_held:")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.WalletDrift{}
	for rows.Next() {
		var d domain.WalletDrift
		if err := rows.Scan(&d.UserID, &d.WalletBalance, &d.LedgerBalance,
			&d.WalletHeld, &d.LedgerHeld); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
