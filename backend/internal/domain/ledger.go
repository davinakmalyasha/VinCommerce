package domain

import "time"

// LedgerEntry is one line of a double-entry journal.
//
// It lives in domain rather than in service because BOTH the service layer (which
// builds journals and validates them) and the repository layer (which writes them)
// need the type, and domain is the only package both are allowed to import.
// Defining it in the service would force the repository to import the service,
// which inverts the dependency direction the whole layering is built on.
type LedgerEntry struct {
	Account  string
	Amount   float64
	Side     string // "debit" or "credit"
	Metadata map[string]any
}

// Ledger journal side constants. The values are the same strings the schema's
// CHECK constraints accept.
const (
	LedgerSideDebit  = "debit"
	LedgerSideCredit = "credit"
)

// LedgerJournal is a balanced transaction: the header of a set of entries whose
// debits and credits must sum equal.
type LedgerJournal struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	TxType         string    `json:"tx_type"`
	RefType        string    `json:"ref_type,omitempty"`
	RefID          string    `json:"ref_id,omitempty"`
	EffectiveAt    time.Time `json:"effective_at"`
	// ReversalOf is set when this journal corrects an earlier one. Nothing is
	// ever updated or deleted from the journal, so the audit trail is append-only
	// and a mistaken posting is corrected rather than erased.
	ReversalOf string    `json:"reversal_of,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// LedgerEntryRow is a persisted journal line, as read back.
type LedgerEntryRow struct {
	ID          int64          `json:"id"`
	JournalID   string         `json:"journal_id"`
	AccountCode string         `json:"account_code"`
	Amount      float64        `json:"amount"`
	Side        string         `json:"side"`
	Currency    string         `json:"currency"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// TrialBalanceRow is one line of a trial balance.
//
// Balance is stored signed by the account's normal side, so a positive number
// reads the way an accountant expects: positive on an asset means "we hold
// this much", positive on a liability means "we owe this much".
type TrialBalanceRow struct {
	AccountCode string  `json:"account_code"`
	Name        string  `json:"name,omitempty"`
	Class       string  `json:"class,omitempty"`
	NormalSide  string  `json:"normal_side,omitempty"`
	IsSystem    bool    `json:"is_system"`
	Balance     float64 `json:"balance"`
}

// DriftRow is a cached balance that disagrees with the entries it was derived
// from.
type DriftRow struct {
	AccountCode string  `json:"account_code"`
	Cached      float64 `json:"cached"`
	Expected    float64 `json:"expected"`
}

// UnbalancedJournal is a journal whose debits and credits do not sum equal.
//
// The schema's deferred constraint trigger should make this impossible. It is
// still reported, because a constraint that has never been observed firing is a
// belief rather than evidence, and this is the observation.
type UnbalancedJournal struct {
	JournalID      string  `json:"journal_id"`
	IdempotencyKey string  `json:"idempotency_key"`
	TxType         string  `json:"tx_type"`
	Debits         float64 `json:"debits"`
	Credits        float64 `json:"credits"`
	Imbalance      float64 `json:"imbalance"`
}

// WalletDrift is a user whose `wallets` row -- the balance the application
// actually reads -- disagrees with their ledger account.
//
// This is the drift a user would notice: a seller whose ledger says Rp50,000 and
// whose wallet says Rp0.
type WalletDrift struct {
	UserID        string  `json:"user_id"`
	WalletBalance float64 `json:"wallet_balance"`
	LedgerBalance float64 `json:"ledger_balance"`
	WalletHeld    float64 `json:"wallet_held"`
	LedgerHeld    float64 `json:"ledger_held"`
}

// LedgerReconciliation is the report that proves the ledger is right.
//
// Before this, the system had a single-sided movement log that could not be
// reconciled to cash at all: tracing one Rp100,000 order at 2% commission, the
// books said Rp2,000 while the world held Rp100,000. This is the check that
// makes the difference between "we have a ledger" and "we can prove it".
type LedgerReconciliation struct {
	GeneratedAt        time.Time           `json:"generated_at"`
	Clean              bool                `json:"clean"`
	BalanceCacheDrift  []DriftRow          `json:"balance_cache_drift,omitempty"`
	Unbalanced         []UnbalancedJournal `json:"unbalanced_journals,omitempty"`
	WalletDrift        []WalletDrift       `json:"wallet_drift,omitempty"`
	BalanceCacheRows   int                 `json:"balance_cache_drift_count"`
	UnbalancedJournals int                 `json:"unbalanced_journal_count"`
	WalletDriftRows    int                 `json:"wallet_drift_count"`
}
