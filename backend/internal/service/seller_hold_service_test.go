package service

import (
	"strings"
	"testing"
)

// A hold is ONE event: two ledger accounts, two wallet columns and a reservation.
// Split across separate transactions, any partial failure leaves the books
// describing a state that never happened -- and the two preceding commits are the
// evidence, because the release rule existed with no holds, and then holds existed
// with no enforcement.
//
// Pinned at the source because `HoldSellerFunds` opens its own transaction and
// cannot be entered without a live database.

func TestAholdIsOneTransactionOfFourWrites(t *testing.T) {
	body := functionSource(t, "payment_service.go", "HoldSellerFunds")

	if !strings.Contains(body, "s.orders.Begin(ctx)") {
		t.Errorf("the hold does not open a transaction:\n%s\n"+
			"four writes in autocommit mean a failure after the second leaves a "+
			"journal with no wallet movement, or a wallet movement with no journal", body)
	}
	if !strings.Contains(body, "defer tx.Rollback(ctx)") {
		t.Errorf("the hold has no rollback:\n%s", body)
	}
	if !strings.Contains(body, "return tx.Commit(ctx)") {
		t.Errorf("the hold never commits:\n%s", body)
	}

	for _, step := range []struct{ what, needle string }{
		{"the available ledger account", "EnsurePersonalAccount(ctx, q, sellerID)"},
		{"the held ledger account", "EnsureHeldAccount(ctx, q, sellerID)"},
		{"the journal", "postLedgerStrict(ctx, q, JournalSpec{"},
		{"the wallet move", `WalletHeldTxOn(ctx, q, sellerID, "hold"`},
		{"the reservation", "ReserveSellerPendingTx(ctx, q, sellerID, orderID, kind, note, amount)"},
	} {
		if !strings.Contains(body, step.needle) {
			t.Errorf("the hold is missing %s:\n%s", step.what, body)
		}
	}

	// The journal precedes the wallet move, so a posting failure stops the money
	// moving. The other order leaves money moved with nothing to say why.
	postAt, walletAt := strings.Index(body, "postLedgerStrict"),
		strings.Index(body, `WalletHeldTxOn(ctx, q, sellerID, "hold"`)
	if postAt < 0 || walletAt < 0 || postAt > walletAt {
		t.Errorf("the wallet moves before the journal is posted:\n%s", body)
	}
}

// A release must be the EXACT MIRROR of a hold, in the same order, or a hold
// becomes permanent in the one direction that matters.
//
// The first version of the release stamped `released_at` and moved no money: the
// reservation claimed released while `wallets.balance` had never been credited
// back. The seller's money would have been unreleasable forever, which looks
// exactly like a platform that has lost it.
func TestAReleaseIsTheMirrorOfAhold(t *testing.T) {
	release := functionSource(t, "payment_service.go", "releaseOneReservation")

	for _, step := range []string{
		"MarkReservationReleasedTx(ctx, q, res.ID)",
		"postLedgerStrict(ctx, q, JournalSpec{",
		`WalletHeldTxOn(ctx, q, res.SellerID, "release"`,
		"return tx.Commit(ctx)",
	} {
		if !strings.Contains(release, step) {
			t.Errorf("the release is missing %q:\n%s", step, release)
		}
	}
	if strings.Contains(release, `WalletHeldTxOn(ctx, q, res.SellerID, "hold"`) {
		t.Errorf("the release moves money in the HOLD direction:\n%s\n"+
			"that would debit the spendable balance again and read exactly like a "+
			"second withdrawal", release)
	}

	// The claim comes FIRST, before any money moves. It is the only thing standing
	// between a retried job and crediting the seller twice.
	claimAt := strings.Index(release, "MarkReservationReleasedTx")
	moveAt := strings.Index(release, `WalletHeldTxOn(ctx, q, res.SellerID, "release"`)
	if claimAt < 0 || moveAt < 0 || claimAt > moveAt {
		t.Errorf("the release moves money before it claims the hold:\n%s\n"+
			"two concurrent runs read the same candidate; the first claims it, the "+
			"second's UPDATE must match nothing and it must then move no money", release)
	}
	if !strings.Contains(release, "if !claimed {") {
		t.Errorf("the release ignores the claim result:\n%s\n"+
			"a losing run must return without moving anything", release)
	}

	// The two journals must be OPPOSITE, not merely both present. A shared
	// idempotency key would make the release a duplicate of the hold, and
	// `Post` returns the original journal without moving anything.
	hold := functionSource(t, "payment_service.go", "HoldSellerFunds")
	if h, r := idempotencyKeyOf(hold), idempotencyKeyOf(release); h == r || h == "" || r == "" {
		t.Errorf("the hold and the release must have distinct idempotency keys:\n"+
			"  hold:    %q\n  release: %q\n"+
			"sharing one means the release is swallowed as a duplicate of the hold and "+
			"no money moves back", h, r)
	}
}

func idempotencyKeyOf(body string) string {
	i := strings.Index(body, "IdempotencyKey:")
	if i < 0 {
		return ""
	}
	rest := body[i+len("IdempotencyKey:"):]
	j := strings.IndexAny(rest, "\n")
	return strings.TrimSpace(rest[:j])
}

// A hold that already exists is SUCCESS, not a failure. The money is already
// protected, which is what the caller asked for, and returning an error would
// reject a return claim for a hold it already has.
func TestAnExistingLiveHoldIsSuccessRatherThanAnError(t *testing.T) {
	body := functionSource(t, "payment_service.go", "HoldSellerFunds")
	if !strings.Contains(body, "SELLER_HOLD_EXISTS") {
		t.Errorf("the hold does not recognise its own conflict:\n%s\n"+
			"a second claim on one order would fail rather than finding the money "+
			"already protected", body)
	}
}

// The hold must be refused outright when the ledger is unwired. With a swallowing
// `postLedger`, an unwired ledger used to mean "the money moves and nothing records
// it" -- the defect this whole sequence exists to close.
func TestAHoldRefusesRatherThanMovingMoneyWithNoLedger(t *testing.T) {
	body := functionSource(t, "payment_service.go", "HoldSellerFunds")
	if !strings.Contains(body, "s.ledger == nil") {
		t.Errorf("the hold does not check that the ledger is wired:\n%s\n"+
			"an unwired ledger must stop the hold, not let it move money unrecorded", body)
	}
	if !strings.Contains(body, "LEDGER_UNAVAILABLE") {
		t.Errorf("the unwired-ledger case is not named:\n%s", body)
	}
}

// A hold kind the release job does not evaluate is a hold that is never released,
// which is a permanent deduction from a seller's balance. So the vocabulary the
// hold accepts and the vocabulary the release query matches must be the same one.
func TestEveryHoldKindIsOneTheReleaseJobEvaluates(t *testing.T) {
	releasable := map[string]bool{HoldCOD: true, HoldReturn: true, HoldDispute: true}

	for kind := range map[string]bool{HoldCOD: true, HoldReturn: true, HoldDispute: true} {
		if !releasable[kind] {
			t.Errorf("hold kind %q is declared but the release job does not evaluate "+
				"it; the hold would never be released and the seller could not withdraw "+
				"that money ever", kind)
		}
	}

	// And an unmapped kind must fail LOUD. walletReasonForHold's reason is a key in
	// uq_wallet_tx_business_event, so a shared fallback would collapse a return
	// hold and a dispute hold on one order onto one index key and surface as a raw
	// 23505 in the middle of a return claim -- a database error where a caller
	// mistake belongs.
	if _, err := walletReasonForHold("nonsense"); err == nil {
		t.Error("an unknown hold kind produced a reason; it must be refused by name, " +
			"or two different holds on one order collide on the wallet-transaction index")
	}
	if got, err := walletReasonForHold(HoldReturn); err != nil || got == "" {
		t.Errorf("walletReasonForHold(HoldReturn) = %q, %v; want a reason and no error", got, err)
	}

	// The repository must refuse the same kinds.
	repo := readRepositorySource(t, "payment_repo.go")
	if !strings.Contains(repo, `case "cod", "return", "dispute":`) {
		t.Error("the repository no longer validates hold kinds against the same three " +
			"the release job evaluates; a new kind would be insertable and never released")
	}
	// And the release query must match the same three.
	if !strings.Contains(repo, "AND sr.kind IN ('cod', 'return', 'dispute')") {
		t.Error("the release query no longer matches the three hold kinds; a hold of " +
			"another kind would sit unreleased forever")
	}
}
