package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// Account codes. These are the strings the rest of the application posts
// against; they are constants rather than bare literals at each call site
// because a typo in an account code is a silent misposting -- the journal still
// balances, the money just lands in the wrong place, and nothing complains.
//
// They match the rows seeded by migration 00043. A code that is not in that list
// is rejected by the foreign key, so the two cannot drift without the insert
// failing.
const (
	// assets
	AccBankClearing         = "bank_clearing"
	AccGatewayClearing      = "gateway_clearing"
	AccCodReceivable        = "cod_receivable"
	AccRefundPayable        = "refund_payable"
	AccChargebackReceivable = "chargeback_receivable"
	// liabilities
	AccEscrowHeld       = "escrow_held"
	AccSellerAvailable  = "seller_available"
	AccSellerPending    = "seller_pending"
	AccSellerHeld       = "seller_held"
	AccTaxPayablePpn    = "tax_payable_ppn"
	AccTaxWithheldPph22 = "tax_withheld_pph22"
	// equity
	AccPlatformEquity = "platform_equity"
	// revenue
	AccPlatformCommission = "platform_commission"
	AccPlatformAds        = "platform_ads"
	AccPlatformPromo      = "platform_promo"
	AccOtherIncome        = "other_income"
	// expenses
	AccGatewayFee     = "gateway_fee"
	AccRefundWriteoff = "refund_writeoff"
	AccChargebackLoss = "chargeback_loss"
	AccRoundingDust   = "rounding_dust"
)

// personalAccountPrefix is the reserved namespace for per-user accounts. A
// personal account is named `seller_available:<user uuid>` so a posting needs no
// lookup, and the naming makes "which entries touch this seller" answerable from
// a LIKE on the account code without a join.
const personalAccountPrefix = AccSellerAvailable + ":"

// heldAccountPrefix is the same idea for money a seller has earned and may not yet
// spend: `seller_held:<user uuid>`.
//
// A SECOND namespace, not a flag on the first, because the two are different
// claims on the same money. `seller_available` is money the platform owes a seller
// and the seller may withdraw; `seller_held` is money the platform still might
// have to reverse, so it may not. Collapsing them into one account with a state
// column would make "what can this seller withdraw" a question about a row rather
// than a balance, and the balance is what the application reads.
//
// It also matches the reconciliation, which compares `wallets.held_balance`
// against the balance of `seller_held:<user id>` (LedgerRepository.reconcileWallets).
// That comparison has been reporting "clean" since it was written, because both
// sides were always zero and nothing ever posted here.
const heldAccountPrefix = AccSellerHeld + ":"

// PersonalAccount is the ledger account code for a user's spendable balance.
func PersonalAccount(userID string) string { return personalAccountPrefix + userID }

// SellerHeldAccount is the ledger account code for a user's HELD balance: money
// earned and not yet withdrawable because a return, dispute or uncollected COD
// order could still reverse it.
func SellerHeldAccount(userID string) string { return heldAccountPrefix + userID }

// Ledger entry sides. Re-exported from domain rather than redeclared, so the
// service and the schema cannot disagree about the spelling and a typo cannot
// compile.
const (
	LedgerSideDebit  = domain.LedgerSideDebit
	LedgerSideCredit = domain.LedgerSideCredit
)

// Journal types, recorded so a trial balance can be grouped and a reconciliation
// report read without joining through orders and intents.
const (
	TxTypeOrderPlaced   = "order_placed"
	TxTypePayment       = "payment"
	TxTypeEscrowRelease = "escrow_release"
	TxTypeRefund        = "refund"
	TxTypePayout        = "payout"
	TxTypeAdjustment    = "adjustment"
	TxTypeSettlement    = "settlement"
	TxTypeReversal      = "reversal"
	// TxTypeSellerHold is money moved out of a seller's spendable balance and into
	// their held balance. A separate type rather than `adjustment` so a trial
	// balance can answer "how much of a seller's earnings is currently not
	// withdrawable" without a second query, and so a hold is visible as its own
	// kind of event rather than looking like an unexplained adjustment.
	TxTypeSellerHold = "seller_hold"
)

// LedgerEntry is a journal line. It is an ALIAS of domain.LedgerEntry, not a
// second type: the type lives in domain because both the service layer (which
// builds and validates journals) and the repository layer (which writes them)
// need it, and domain is the only package both may import. Declaring it here
// would force the repository to import the service, inverting the layering the
// whole structure is built on.
type LedgerEntry = domain.LedgerEntry

// Debit builds a debit entry, rounded to whole rupiah.
func Debit(account string, amount float64) LedgerEntry {
	return LedgerEntry{Account: account, Amount: moneyRound(amount), Side: LedgerSideDebit}
}

// Credit builds a credit entry, rounded to whole rupiah.
func Credit(account string, amount float64) LedgerEntry {
	return LedgerEntry{Account: account, Amount: moneyRound(amount), Side: LedgerSideCredit}
}

// WithMetadata returns a copy of the entry with metadata attached.
//
// A function rather than a method because LedgerEntry is an alias of a domain
// type, and Go does not allow methods on a non-local type. The call sites read
// the same either way: Debit(...).WithMetadata(...).
//
// Metadata is what lets an operator answer "which journal moved this?" without a
// schema change: order id, intent id, dispute id, gateway reference.
func WithMetadata(e LedgerEntry, m map[string]any) LedgerEntry {
	e.Metadata = m
	return e
}

// LedgerService posts double-entry journals.
//
// Why this exists: `wallet_transactions` is a single-sided running-balance log
// with no account dimension, no counterparty and no external leg. Every movement
// between the outside world and a wallet is unrecorded, so SUM(wallets.balance)
// cannot be reconciled to cash. Tracing one Rp100,000 order at 2% commission:
//
//	capture -> writes NO ledger rows at all
//	release -> credits the seller Rp98,000 and the platform Rp2,000
//	payout  -> debits the seller Rp98,000
//
// The books say Rp2,000. The world holds Rp100,000. The missing Rp98,000 is a real
// liability to a seller who has already withdrawn it and a real cash asset, and
// it appears on no balance sheet anywhere in this system.
//
// The invariant that makes the new model trustworthy is that every journal sums
// to zero, enforced by a DEFERRABLE CONSTRAINT TRIGGER at COMMIT rather than by Go
// code. That is deliberate: a check in the service layer protects the paths that
// remember to call it; a check in the schema protects all of them, including the
// one somebody writes next year.
type LedgerService struct {
	ledger *repository.LedgerRepository
	logger *slog.Logger
}

// NewLedgerService constructs the service.
func NewLedgerService(ledger *repository.LedgerRepository, logger *slog.Logger) *LedgerService {
	if logger == nil {
		logger = slog.Default()
	}
	return &LedgerService{ledger: ledger, logger: logger}
}

// JournalSpec is the input to Post.
type JournalSpec struct {
	// IdempotencyKey is the business key. Required: a journal without one cannot
	// be safely retried, and every caller of Post is a retryable operation (a
	// webhook delivered twice, a worker job re-run after a partial failure).
	IdempotencyKey string
	TxType         string
	RefType        string
	RefID          string
	Entries        []LedgerEntry
	Note           string
	// EffectiveAt is an optional RFC3339 timestamp; empty means now(). It exists
	// so a settlement can be backdated to the gateway's own date rather than the
	// date it was imported, which is the difference between a balance sheet and a
	// guess.
	EffectiveAt string
	// ReversalOf is the journal id this one reverses. A reversal is a NEW journal
	// pointing back at its original; nothing is ever UPDATEd or DELETEd, so a
	// mistaken posting is corrected rather than erased.
	ReversalOf string
}

// Post writes a balanced journal.
//
// It refuses an unbalanced journal in Go, with a message naming the accounts, even
// though the database would also reject it. The Go check exists because the
// database error arrives as a constraint violation that says only "journal X is
// unbalanced by Y" -- which tells you the journal but not the mistake, and this
// is called from six different money paths.
func (s *LedgerService) Post(ctx context.Context, q repository.Querier, spec JournalSpec) (*domain.LedgerJournal, error) {
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		return nil, domain.E(domain.KindInvalid, "MISSING_IDEMPOTENCY_KEY",
			"a journal without an idempotency key cannot be safely retried")
	}
	entries := normaliseEntries(spec.Entries)
	if len(entries) == 0 {
		return nil, domain.E(domain.KindInvalid, "EMPTY_JOURNAL",
			"a journal with no entries is a caller bug, not a no-op")
	}
	if err := validateBalanced(entries); err != nil {
		return nil, err
	}
	return s.ledger.PostJournal(ctx, q, spec.IdempotencyKey, spec.TxType,
		spec.RefType, spec.RefID, spec.Note, spec.EffectiveAt, spec.ReversalOf, entries)
}

// normaliseEntries rounds to whole rupiah and drops zero-amount lines.
//
// Zero is dropped because `CHECK (amount > 0)` rejects it, and a caller
// computing a split where one leg rounds to zero should not have to special-case
// it at six call sites.
//
// Negative is NOT dropped: a negative debit is a different statement from an
// absent one, and discarding it would turn a sign error into a missing entry --
// which still balances, and is wrong.
func normaliseEntries(in []LedgerEntry) []LedgerEntry {
	out := make([]LedgerEntry, 0, len(in))
	for _, e := range in {
		e.Amount = moneyRound(e.Amount)
		if e.Amount == 0 {
			continue
		}
		out = append(out, e)
	}
	return out
}

// validateBalanced is the Go-side pre-check.
//
// Account codes are sorted before reporting so the message is deterministic.
// Without that, a bug that made two callers build the same entry list in
// different orders would produce two different messages for one problem, which
// is a genuinely unpleasant thing to debug at 3am.
func validateBalanced(entries []LedgerEntry) error {
	var debits, credits float64
	seen := map[string]bool{}
	for _, e := range entries {
		if strings.TrimSpace(e.Account) == "" {
			return domain.E(domain.KindInvalid, "EMPTY_ACCOUNT",
				"a ledger entry must name an account")
		}
		if seen[e.Account] {
			// Two entries for one account in one journal is allowed by the schema
			// but is almost always a caller that meant to combine them, and
			// combining them here makes the trial balance one row instead of two.
			return domain.E(domain.KindInvalid, "DUPLICATE_ACCOUNT",
				fmt.Sprintf("journal posts %q more than once; combine the amounts", e.Account))
		}
		seen[e.Account] = true
		switch e.Side {
		case LedgerSideDebit:
			debits = moneyRound(debits + e.Amount)
		case LedgerSideCredit:
			credits = moneyRound(credits + e.Amount)
		default:
			return domain.E(domain.KindInvalid, "BAD_SIDE",
				fmt.Sprintf("entry for %q has side %q, want debit or credit", e.Account, e.Side))
		}
	}
	if debits != credits {
		codes := make([]string, 0, len(entries))
		for _, e := range entries {
			codes = append(codes, e.Account+":"+e.Side)
		}
		sort.Strings(codes)
		return domain.E(domain.KindInvalid, "UNBALANCED_JOURNAL",
			fmt.Sprintf("debits Rp%.0f != credits Rp%.0f in [%s]",
				debits, credits, strings.Join(codes, " ")))
	}
	return nil
}

// EscrowOutstanding is the number a marketplace operator asks for first: how much
// money is being held for other people right now.
//
// Before this existed the only way to approximate it was to sum captured-but-not-
// released payment intents, which silently omits COD in transit, disputes, and any
// balance a refund has already drawn down.
func (s *LedgerService) EscrowOutstanding(ctx context.Context, q repository.Querier) (float64, error) {
	return s.ledger.BalanceOf(ctx, q, AccEscrowHeld)
}

// EnsurePersonalAccount makes sure a user's ledger account exists.
//
// Called before a user's first posting. The alternative is a trigger that creates
// it on demand, which hides a real question -- "is this user allowed to hold
// money?" -- behind a database side effect.
func (s *LedgerService) EnsurePersonalAccount(ctx context.Context, q repository.Querier, userID string) error {
	return s.ledger.EnsurePersonalAccount(ctx, q, userID)
}

// EnsureHeldAccount makes sure a user's HELD ledger account exists.
//
// Separate from EnsurePersonalAccount rather than a flag, because the two
// accounts answer different questions and a `held bool` invites the caller to pass
// the wrong one: every posting that touches a seller's withdrawable balance needs
// the available account to exist, and a hold needs both.
func (s *LedgerService) EnsureHeldAccount(ctx context.Context, q repository.Querier, userID string) error {
	return s.ledger.EnsureHeldAccount(ctx, q, userID)
}

// TrialBalance returns every account balance, sorted by code.
func (s *LedgerService) TrialBalance(ctx context.Context, q repository.Querier) ([]domain.TrialBalanceRow, error) {
	return s.ledger.TrialBalance(ctx, q)
}

// Reconcile proves the derived caches agree with the ledger.
//
// Two things can drift: `account_balances` (updated on every post) and `wallets`
// (the pre-existing balance column that the whole application reads). The
// journal is the truth; these are caches. A cache that is never checked against
// its source is not a cache, it is a second opinion.
//
// This is the function that turns "we added a ledger" into "we can prove the
// ledger is right".
func (s *LedgerService) Reconcile(ctx context.Context, q repository.Querier) (*domain.LedgerReconciliation, error) {
	return s.ledger.Reconcile(ctx, q)
}

// ReconcileAll runs the report over the ledger's own pool, for callers with no
// transaction to join -- chiefly the scheduled job that makes the cache
// trustworthy rather than merely present.
func (s *LedgerService) ReconcileAll(ctx context.Context) (*domain.LedgerReconciliation, error) {
	return s.ledger.ReconcileAll(ctx)
}
