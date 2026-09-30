package repository

import (
	"context"
	"strings"
	"testing"
)

// The hold has to MOVE MONEY, or it is a note.
//
// A `seller_reservations` row on its own changes nothing a seller can feel:
// `RequestPayout` debits `wallets.balance` and reads nothing else. So the row is
// written, the release job updates it, and throughout both commits the money
// stayed withdrawable and the fraud control controlled nothing. What makes it real
// is the third write: `balance -= amount, held_balance += amount`, in the same
// statement as the wallet_transactions row a seller will read.
//
// These drive the real method and assert on the SQL it runs, because a predicate
// that reads plausibly is exactly the thing that failed here -- the old
// `WHERE balance >= $3` is correct for a withdrawal and says nothing about a hold.

// findStmt returns the first recorded statement containing `needle`.
//
// By CONTENT rather than by index: `WalletHeldTxOn` runs three statements (ensure
// the wallet row, update the two balances, append the statement line), so an
// index-based assertion silently retargets itself whenever an unrelated statement
// is inserted above. A test that quietly stops testing is the failure mode this
// whole workstream keeps meeting.
func findStmt(t *testing.T, q *recordingQuerier, needle string) string {
	t.Helper()
	for _, s := range q.stmts {
		if strings.Contains(s, needle) {
			return s
		}
	}
	t.Fatalf("no statement containing %q was run; got:\n%s", needle, strings.Join(q.stmts, "\n---\n"))
	return ""
}

// A hold must debit the SPENDABLE balance, not merely touch the row. If it only
// incremented held_balance the money would be double-counted and the seller would
// be short by the hold; if it only debited balance the reconciliation against
// `seller_held:` would report drift forever.
func TestAholdMovesMoneyBetweenTheTwoWalletBalances(t *testing.T) {
	q := &recordingQuerier{row: twoColumnRow{}}
	repo := &PaymentRepository{}

	if err := repo.WalletHeldTxOn(context.Background(), q, "seller-1", "hold",
		WalletReasonHoldReturn, 30000); err != nil {
		t.Fatalf("WalletHeldTxOn: %v", err)
	}

	sql := findStmt(t, q, "UPDATE wallets")
	if !strings.Contains(sql, "balance      = balance      + CASE") ||
		!strings.Contains(sql, "held_balance = held_balance + CASE") {
		t.Errorf("the hold does not move value between the two balances:\n%s\n"+
			"a hold that only writes held_balance makes the reconciliation compare "+
			"wallets.held_balance against seller_held: and report drift; one that "+
			"only debits balance leaves the money counted twice", sql)
	}

	// The direction must be a NAMED value, and the spendable leg must be the one
	// that goes down. A boolean here is how the signs get swapped: a released hold
	// that debited the spendable balance would look exactly like a withdrawal.
	if !strings.Contains(sql, `$2::varchar = 'hold' THEN -$3 ELSE $3`) {
		t.Errorf("the spendable leg is not signed by direction:\n%s\n"+
			"a hold must DEBIT balance and a release must CREDIT it", sql)
	}
	if !strings.Contains(sql, `$2::varchar = 'hold' THEN  $3 ELSE -$3`) {
		t.Errorf("the held leg is not the mirror of the spendable leg:\n%s\n"+
			"holding credits held_balance and releasing debits it; anything else "+
			"leaves the two columns disagreeing about how much is held", sql)
	}
}

// A hold must be refused when the seller cannot cover it, and that refusal must be
// a NAMED conflict rather than a bare "no rows". A COD order whose cash is still
// with the courier is a real and expected version of this, and it arrives as
// INSUFFICIENT_BALANCE on a call the escrow release makes deliberately.
func TestAholdTheSellerCannotCoverIsRefusedByName(t *testing.T) {
	q := &recordingQuerier{row: noRowsRow{}}
	repo := &PaymentRepository{}

	err := repo.WalletHeldTxOn(context.Background(), q, "seller-1", "hold",
		WalletReasonHoldCOD, 50000)
	if err == nil {
		t.Fatal("a hold larger than the balance succeeded; the seller would be left " +
			"with a negative held balance nobody can explain")
	}
	if !strings.Contains(err.Error(), "INSUFFICIENT_BALANCE") {
		t.Errorf("error = %v, want INSUFFICIENT_BALANCE naming the shortfall", err)
	}
	if !strings.Contains(q.sql, "balance >= $3") {
		t.Errorf("the hold is not bounded by the spendable balance:\n%s", q.sql)
	}
}

// The release side must be bounded by the HELD balance, not the spendable one. A
// release larger than the hold would otherwise make held_balance negative -- and
// `wallets.held_balance >= 0` is a CHECK, so it would surface as a constraint
// violation rather than the named error an operator can act on.
func TestAReleaseIsBoundedByTheHeldBalanceNotTheSpendableOne(t *testing.T) {
	q := &recordingQuerier{row: noRowsRow{}}
	repo := &PaymentRepository{}

	err := repo.WalletHeldTxOn(context.Background(), q, "seller-1", "release",
		WalletReasonHoldRelease, 50000)
	if err == nil {
		t.Fatal("a release larger than the held balance succeeded")
	}
	if !strings.Contains(err.Error(), "HELD_BALANCE_UNDERFLOW") {
		t.Errorf("error = %v, want HELD_BALANCE_UNDERFLOW; a release that cannot "+
			"complete must say the reservation and the wallet disagree", err)
	}
	if !strings.Contains(q.sql, "held_balance >= $3") {
		t.Errorf("the release is not bounded by the held balance:\n%s\n"+
			"without this the release is checked against the seller's SPENDABLE "+
			"balance, which is the wrong account entirely", q.sql)
	}
}

// The wallet_transactions row is what a seller sees on their statement, so it has
// to exist and it has to record the spendable figure -- the one they recognise.
func TestAholdIsVisibleOnTheSellersStatement(t *testing.T) {
	q := &recordingQuerier{row: twoColumnRow{}}
	repo := &PaymentRepository{}

	if err := repo.WalletHeldTxOn(context.Background(), q, "seller-1", "hold",
		WalletReasonHoldReturn, 30000); err != nil {
		t.Fatalf("WalletHeldTxOn: %v", err)
	}
	row := findStmt(t, q, "INSERT INTO wallet_transactions")
	if !strings.Contains(row, "balance_after") {
		t.Errorf("the wallet row records no balance_after; a seller reading their " +
			"statement cannot see what the hold left them")
	}
	// ref_id NULL keeps this insert clear of uq_wallet_tx_business_event, which is
	// UNIQUE on (wallet_id, ref_id, kind, reason). Populating it with the order id
	// would make a second hold of the same kind for one order fail as a raw 23505
	// in the middle of a return claim.
	if !strings.Contains(row, "NULL)") && !strings.Contains(row, "NULL,") {
		t.Errorf("the wallet row does not leave ref_id NULL:\n%s\n"+
			"that index is the one thing standing between a legitimate second hold "+
			"and a raw constraint violation", row)
	}
}

// A hold must be recorded once per order per kind, by the database. Two concurrent
// claims both read "none exists" and both write, and taking the same hold twice
// holds the money twice while releasing it once.
func TestOneLiveHoldPerOrderPerKindIsADatabaseGuarantee(t *testing.T) {
	var body strings.Builder
	entries, err := readMigrationDir(t)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.name, ".sql") {
			body.WriteString(e.text)
		}
	}
	all := body.String()

	if !strings.Contains(all, "idx_seller_reservations_live_order_kind") {
		t.Fatal("no index makes a live hold unique per order and kind")
	}
	// Partial on released_at: without it, a seller whose first claim was resolved
	// could never hold that money again, and a released hold is history.
	if !strings.Contains(all, "WHERE order_id IS NOT NULL AND released_at IS NULL") {
		t.Error("the live-hold index is not partial on `released_at IS NULL`; a " +
			"released hold is history, and the constraint is about money held NOW")
	}
	// And the application must use it rather than reading first.
	repo := &PaymentRepository{}
	q := &recordingQuerier{row: idRow{}}
	if err := repo.ReserveSellerPendingTx(context.Background(), q, "s1", "o1", "return", "n", 1000); err != nil {
		t.Fatalf("ReserveSellerPendingTx: %v", err)
	}
	if !strings.Contains(q.sql, "ON CONFLICT (order_id, kind)") {
		t.Errorf("the hold does not use the index:\n%s\n"+
			"a SELECT-then-INSERT guard in Go cannot close the window two concurrent "+
			"claims both pass through", q.sql)
	}
	if !strings.Contains(q.sql, "RETURNING id") {
		t.Errorf("the insert does not RETURN its id:\n%s\n"+
			"without it the caller cannot tell an insert from a conflict, and the "+
			"first version guessed with a one-second timestamp window", q.sql)
	}
}
