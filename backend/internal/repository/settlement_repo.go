package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/domain"
)

// Gateway settlements: the record of money that actually moved to or from a
// payment gateway.
//
// This is the leg that ties the internal ledger to reality. Without it the
// platform can compare its books to itself -- and it can do that very accurately,
// which is the trap: a self-consistent ledger that has never been compared to a
// bank statement agrees with itself and with nothing else. Tracing the original
// problem: the books said Rp2,000 while the platform held Rp100,000, and no
// table anywhere recorded the Rp100,000 arriving. Nothing could contradict
// anything, because there was nothing to contradict it with.
//
// `settled_on` is the GATEWAY's settlement date, not the capture date. Midtrans
// settles T+1 to T+7 by payment method, so a balance sheet that uses the capture
// date overstates cash in flight by however long settlement takes -- which, for a
// marketplace holding other people's money, is not a rounding difference.

// Settlement import states.
const (
	SettlementImportPending  = "pending"
	SettlementImportImported = "imported"
	SettlementImportPartial  = "partial"
	SettlementImportFailed   = "failed"
)

// Settlement kinds, mirroring the CHECK on the table.
const (
	SettlementCapture    = "capture"
	SettlementPayout     = "payout"
	SettlementRefund     = "refund"
	SettlementFee        = "fee"
	SettlementChargeback = "chargeback"
	SettlementAdjustment = "adjustment"
)

// GatewaySettlement is one movement of cash as the gateway reported it.
type GatewaySettlement struct {
	ID            string
	Gateway       string
	SettlementRef string
	SettledOn     time.Time
	Kind          string
	Gross         float64
	Fee           float64
	Net           float64
	Currency      string
	Raw           map[string]any
	JournalID     string
	ReconciledAt  *time.Time
	CreatedAt     time.Time
}

// SettlementImport is the receipt for a batch of settlements.
//
// Imported files arrive in batches and batches fail as a unit. Without the
// import row, a run that wrote 400 of 500 lines and then died is indistinguishable
// from a run that legitimately had 400 lines -- and re-running it double-counts
// the 400.
type SettlementImport struct {
	ID           string
	BatchRef     string
	Gateway      string
	SettledOn    time.Time
	Source       string
	Status       string
	TotalRows    int
	AcceptedRows int
	RejectedRows int
	Error        string
	ImportedBy   string
	CreatedAt    time.Time
	FinishedAt   *time.Time
}

// RecordSettlement writes one gateway-reported movement.
//
// Idempotent on (gateway, kind, settlement_ref), which is what makes re-importing
// a file safe. The provider can legitimately reuse a reference string for a
// capture and a later refund, so the guard includes the kind -- treating that as
// a duplicate would reject a real refund.
func (r *PaymentRepository) RecordSettlement(ctx context.Context, q Querier, s *GatewaySettlement) (created bool, err error) {
	raw, err := json.Marshal(s.Raw)
	if err != nil {
		return false, err
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO gateway_settlements
		    (gateway, settlement_ref, settled_on, kind, gross, fee, net, currency, raw)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (gateway, kind, settlement_ref) DO NOTHING`,
		s.Gateway, s.SettlementRef, s.SettledOn, s.Kind, s.Gross, s.Fee, s.Net, s.Currency, raw)
	if err != nil {
		return false, err
	}
	// RowsAffected is 0 for a conflict, which is how a re-import is recognised
	// without a second read.
	return tag.RowsAffected() > 0, nil
}

// BeginSettlementImport opens an import receipt.
//
// Written BEFORE the lines are imported, for the same reason a refund row is
// written before the gateway is called: a process that dies mid-import leaves a
// `pending` receipt that says what it was doing, rather than no trace at all.
func (r *PaymentRepository) BeginSettlementImport(ctx context.Context, imp *SettlementImport) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO settlement_imports
		    (batch_ref, gateway, settled_on, source, status, total_rows, imported_by)
		VALUES (NULLIF($1, ''), $2, $3, $4, $5, $6, NULLIF($7, '')::uuid)
		ON CONFLICT (gateway, batch_ref) DO NOTHING
		RETURNING id`,
		imp.BatchRef, imp.Gateway, imp.SettledOn, imp.Source, SettlementImportPending,
		imp.TotalRows, imp.ImportedBy)
	return err
}

// FinishSettlementImport closes a receipt with its outcome.
//
// `partial` is a real state and not an error to hide: 400 of 500 lines imported
// is exactly the case an operator needs to see, because the missing 100 are
// movements of real money the platform does not know about.
func (r *PaymentRepository) FinishSettlementImport(
	ctx context.Context, importID, status string, accepted, rejected int, failure string,
) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE settlement_imports
		   SET status = $2,
		       accepted_rows = $3,
		       rejected_rows = $4,
		       error = NULLIF($5, ''),
		       finished_at = now()
		 WHERE id = $1`, importID, status, accepted, rejected, failure)
	return err
}

// SettlementImportByBatch looks up a receipt by the provider's batch reference.
func (r *PaymentRepository) SettlementImportByBatch(ctx context.Context, gateway, batchRef string) (*SettlementImport, error) {
	var i SettlementImport
	var batch, errMsg *string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(batch_ref, ''), gateway, settled_on, source, status,
		       total_rows, accepted_rows, rejected_rows, COALESCE(error, ''),
		       COALESCE(imported_by::text, ''), created_at, finished_at
		  FROM settlement_imports
		 WHERE gateway = $1 AND batch_ref = $2`, gateway, batchRef).
		Scan(&i.ID, &batch, &i.Gateway, &i.SettledOn, &i.Source, &i.Status,
			&i.TotalRows, &i.AcceptedRows, &i.RejectedRows, &errMsg, &i.ImportedBy, &i.CreatedAt, &i.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if batch != nil {
		i.BatchRef = *batch
	}
	if errMsg != nil {
		i.Error = *errMsg
	}
	return &i, nil
}

// UnreconciledSettlements lists gateway movements not yet matched to a journal,
// oldest first.
//
// "Oldest first" is the whole point of the ordering: a settlement the gateway
// reported days ago and we never matched is money that moved with no entry on
// our side, and it is the one nobody looks at because the queue sorts the other
// way.
func (r *PaymentRepository) UnreconciledSettlements(ctx context.Context, limit int) ([]*GatewaySettlement, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, settlementSelect+`
		 WHERE reconciled_at IS NULL
		 ORDER BY settled_on ASC, created_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*GatewaySettlement{}
	for rows.Next() {
		s, err := scanSettlement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SettlementMatch is a gateway movement resolved against the ledger.
type SettlementMatch struct {
	GatewaySettlement
	// JournalTotal is what the journal actually recorded, and AmountDelta is the
	// signed difference against what the gateway reported.
	JournalTotal float64
	AmountDelta  float64
	IsMatched    bool
	TxType       string
}

// SettlementMatches reads the reconciliation view.
//
// The view exists so "is this matched, and does the amount agree?" is ONE query
// with ONE definition. Left to the job and the admin screen to each write their
// own comparison, and the one that drifts is the one nobody checks -- which is how
// a cache becomes a second opinion.
func (r *PaymentRepository) SettlementMatches(ctx context.Context, unreconciledOnly bool, limit int) ([]*SettlementMatch, error) {
	if limit <= 0 {
		limit = 200
	}
	q := settlementMatchSelect + ` WHERE true`
	if unreconciledOnly {
		q += ` AND NOT is_matched`
	}
	q += ` ORDER BY settled_on ASC LIMIT $1`

	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*SettlementMatch{}
	for rows.Next() {
		var m SettlementMatch
		if err := rows.Scan(&m.ID, &m.Gateway, &m.SettlementRef, &m.SettledOn, &m.Kind,
			&m.Gross, &m.Fee, &m.Net, &m.JournalID, &m.ReconciledAt,
			&m.TxType, &m.JournalTotal, &m.AmountDelta, &m.IsMatched); err != nil {
			return nil, err
		}
		m.Currency = "IDR"
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MarkSettlementReconciled links a settlement to the journal that recorded it.
//
// Fails if the journal is already linked to another settlement: one journal, one
// movement of money. Two settlements sharing a journal would mean the same entry
// is being used to explain two different movements, and nothing else would catch
// it -- the amounts would still foot in isolation.
func (r *PaymentRepository) MarkSettlementReconciled(ctx context.Context, settlementID, journalID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE gateway_settlements
		   SET journal_id = $2, reconciled_at = now()
		 WHERE id = $1 AND reconciled_at IS NULL`, settlementID, journalID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "SETTLEMENT_ALREADY_RECONCILED",
			"settlement is already matched, or no longer exists")
	}
	return nil
}

const settlementSelect = `
	SELECT id::text, gateway, COALESCE(settlement_ref, ''), settled_on, kind,
	       gross::float8, fee::float8, net::float8, currency,
	       raw, COALESCE(journal_id::text, ''), reconciled_at, created_at
	  FROM gateway_settlements`

const settlementMatchSelect = `
	SELECT id::text, gateway, COALESCE(settlement_ref, ''), settled_on, kind,
	       gross, fee, net, COALESCE(journal_id::text, ''), reconciled_at,
	       COALESCE(tx_type, ''), journal_total, amount_delta, is_matched
	  FROM settlement_matches`

func scanSettlement(row rowScanner) (*GatewaySettlement, error) {
	var s GatewaySettlement
	var raw []byte
	if err := row.Scan(&s.ID, &s.Gateway, &s.SettlementRef, &s.SettledOn, &s.Kind,
		&s.Gross, &s.Fee, &s.Net, &s.Currency, &raw, &s.JournalID, &s.ReconciledAt, &s.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s.Raw)
	}
	return &s, nil
}
