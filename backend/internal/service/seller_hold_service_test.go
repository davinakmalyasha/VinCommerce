package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	wrapper := functionSource(t, "payment_service.go", "HoldSellerFunds")
	body := functionSource(t, "payment_service.go", "holdSellerFundsTx")

	// The wrapper owns the transaction; the four writes are in the function it
	// delegates to. Both halves are asserted, because a hold is only one event if
	// the transaction that contains it also contains all of them -- and an earlier
	// version of this test found the split the moment it was made.
	if !strings.Contains(wrapper, "s.orders.Begin(ctx)") {
		t.Errorf("the hold does not open a transaction:\n%s\n"+
			"four writes in autocommit mean a failure after the second leaves a "+
			"journal with no wallet movement, or a wallet movement with no journal", wrapper)
	}
	if !strings.Contains(wrapper, "defer tx.Rollback(ctx)") {
		t.Errorf("the hold has no rollback:\n%s", wrapper)
	}
	if !strings.Contains(wrapper, "return tx.Commit(ctx)") {
		t.Errorf("the hold never commits:\n%s", wrapper)
	}
	if !strings.Contains(wrapper, "holdSellerFundsTx(ctx, tx.Querier()") {
		t.Errorf("the hold does not do its writes inside its own transaction:\n%s\n"+
			"the writes must run against the transaction the wrapper opened, or the "+
			"four of them are four transactions", wrapper)
	}
	if strings.Contains(body, "s.orders.Begin(") {
		t.Errorf("the inner hold opens a transaction of its own:\n%s\n"+
			"it is called from inside a transaction (the COD hold, from "+
			"ReleaseEscrow) and opening another would nest outside the caller's", body)
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

// assertNotDisabled rejects a condition that has been short-circuited to a literal
// false while its text stays in place.
//
// This has now caught THREE mutations, which is why it is a helper rather than an
// assertion written at each site:
//
//	releasePayoutReservationsHandler guarded by `if false`   -- survived the
//	    ordering assertion, because the hold's TEXT stayed exactly where it was
//	M23, `if !wasClaimed` inverted                             -- needed a compiling
//	    mutation for a different reason, but the same blindness
//	M29, `if false && intent.Method == ...`                   -- survived an assertion
//	    looking for the substring `intent.Method == domain.MethodCOD`, which the
//	    disabled condition still contains
//
// A guard that is present, in the right place, with the right text and no behaviour
// is the failure mode this whole workstream keeps rediscovering. Disabling a
// condition is the cheapest possible way to ship a regression, so it gets its own
// check rather than an incidental one.
func assertNotDisabled(t *testing.T, body, where string) {
	t.Helper()
	for _, pattern := range []string{"if false {", "if false &&", "&& false", "if true ||", "|| true {"} {
		if strings.Contains(body, pattern) {
			t.Errorf("%s is short-circuited with %q:\n%s\n"+
				"the code is present and does nothing, which reads exactly like a "+
				"working guard to a reviewer and to a substring assertion alike",
				where, pattern, body)
			return
		}
	}
}

// The COD hold is the case that forced the hold to take a Querier: it must run
// INSIDE `ReleaseEscrow`'s transaction.
//
// Two things go wrong if it does not. The window -- the seller is credited, then
// the money is held, and in between they withdraw the lot -- which is the whole
// fraud. And the place: holding at `CaptureCOD` would try to debit a seller's
// balance that is zero, because the money is still in `escrow_held` until release.
func TestTheCODHoldRunsInsideTheEscrowRelease(t *testing.T) {
	release := functionSource(t, "payment_service.go", "ReleaseEscrow")

	holdAt := strings.Index(release, "holdSellerFundsTx(")
	if holdAt < 0 {
		t.Fatalf("escrow release takes no COD hold:\n%s\n"+
			"ReleaseEscrow is the moment COD money becomes withdrawable, so it is the "+
			"only place a hold can bite; nothing took one", release)
	}
	commitAt := strings.Index(release, "tx.Commit(ctx)")
	if holdAt > commitAt {
		t.Errorf("the COD hold is taken after the release commits:\n%s\n"+
			"a separate transaction leaves a window in which the seller can withdraw "+
			"the entire amount, which is the fraud this prevents", release)
	}
	// COD only. A prepaid order has no courier and no uncollected cash, so holding
	// it would freeze money for a failure mode that cannot happen.
	if !strings.Contains(release, "intent.Method == domain.MethodCOD") {
		t.Errorf("the hold is not restricted to COD:\n%s\n"+
			"a prepaid order has no uncollected cash, so holding its money would "+
			"freeze the seller for a failure that cannot occur", release)
	}
	assertNotDisabled(t, release, "the COD hold condition")
	// The seller's NET, not the gross. A refused delivery does not take back the
	// commission. The call is spread over two lines, so read a window from it rather
	// than trying to balance the parentheses.
	n := 240
	if len(release)-holdAt < n {
		n = len(release) - holdAt
	}
	holdCall := release[holdAt : holdAt+n]
	if !strings.Contains(holdCall, "sellerAmount)") {
		t.Errorf("the COD hold is not the seller's net:\n%s\n"+
			"holding the gross would freeze the platform's own commission and make "+
			"a bad COD look like a worse one", holdCall)
	}
	if strings.Contains(holdCall, "intent.Amount)") {
		t.Errorf("the COD hold is the GROSS rather than the seller's net:\n%s", holdCall)
	}
	// And it must be skipped when the net is zero, rather than writing a
	// zero-amount hold that the `amount > 0` CHECK would reject outright.
	if !strings.Contains(release, "sellerAmount > 0") {
		t.Errorf("the COD hold is taken even when the seller is owed nothing:\n%s\n"+
			"a zero-amount reservation is rejected by the `amount > 0` CHECK, so this "+
			"would fail the whole release for a commission-only order", release)
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
	hold := functionSource(t, "payment_service.go", "holdSellerFundsTx")
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
	body := functionSource(t, "payment_service.go", "holdSellerFundsTx")
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
	body := functionSource(t, "payment_service.go", "holdSellerFundsTx")
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

// A failed posting must PROPAGATE on the strict path, not merely be called.
//
// The four-write test above asserts `postLedgerStrict` is called; that is not the
// same thing, and a mutation that turned the strict function back into the
// swallowing one survived it -- because the NAME was still there. So this drives
// the propagation directly.
//
// `Post` validates the entries before it touches the repository, so an unbalanced
// journal fails with no database and no fake: the error comes from the same
// validation a real bad journal would hit, and postLedgerStrict must hand it back.
func TestAFailedJournalStopsTheMoneyMoving(t *testing.T) {
	svc := &PaymentService{ledger: NewLedgerService(nil, nil)}

	// One entry: debits do not equal credits, so Post rejects it.
	unbalanced := JournalSpec{
		IdempotencyKey: "test:unbalanced",
		TxType:         TxTypeSellerHold,
		Entries:        []LedgerEntry{Debit(PersonalAccount(testUserID), 50000)},
	}

	err := svc.postLedgerStrict(t.Context(), &permissiveQuerier{}, unbalanced)
	if err == nil {
		t.Fatal("postLedgerStrict returned no error for a journal that does not " +
			"balance; the caller would move the money with nothing recording it, which " +
			"is exactly what the strict variant exists to prevent")
	}
}

// And the swallowing variant must still swallow, or the distinction is decoration.
//
// `postLedger` is correct for a movement that has already committed: returning the
// error would tell the caller it failed and invite a retry of money that already
// moved. That reasoning only holds there, so the two behaviours have to be
// genuinely different -- and if someone ever unified them, one of the two paths
// silently starts doing the other's job.
func TestTheSwallowingPostStillSwallowsAndTheStrictOneDoesNot(t *testing.T) {
	svc := &PaymentService{ledger: NewLedgerService(nil, nil)}
	spec := JournalSpec{
		IdempotencyKey: "test:unbalanced",
		TxType:         TxTypeSellerHold,
		Entries:        []LedgerEntry{Debit(PersonalAccount(testUserID), 50000)},
	}

	// A nil logger-free service with no ledger: the swallow path logs and returns.
	bare := &PaymentService{}
	bare.postLedger(t.Context(), &permissiveQuerier{}, spec) // must not panic

	if err := svc.postLedgerStrict(t.Context(), &permissiveQuerier{}, spec); err == nil {
		t.Error("the strict path swallowed a posting failure; the two helpers are " +
			"then the same function and the strict one is a name with no behaviour")
	}
}

// The payout lag bounds are DUPLICATED -- config cannot import service, and the two
// would otherwise drift.
//
// Duplication is acceptable only if something compares the copies, so this does. It
// reads both files rather than importing, for the same reason the account prefixes
// are compared by source: the alternative is a dependency the import-boundary check
// exists to forbid. A check that cannot run is worse than the duplication.
func TestConfigPayoutLagBoundsMatchTheService(t *testing.T) {
	cfgSrc, err := os.ReadFile(filepath.Join("..", "config", "config.go"))
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	svcSrc := readSource(t, "payment_service.go")

	// The service's bounds are constants, so compare against their VALUES rather
	// than their source text -- a test that matched the literal would keep passing
	// after the constant changed.
	minAt := strings.Index(svcSrc, "MinPayoutLagDays = ")
	maxAt := strings.Index(svcSrc, "MaxPayoutLagDays = ")
	if minAt < 0 || maxAt < 0 {
		t.Fatal("the payout lag bounds are not declared in payment_service.go")
	}
	min := valueAfter(svcSrc, "MinPayoutLagDays = ")
	max := valueAfter(svcSrc, "MaxPayoutLagDays = ")

	for _, want := range []string{fmt.Sprintf("%d", min), fmt.Sprintf("%d", max)} {
		if !strings.Contains(string(cfgSrc), want) {
			t.Errorf("config.Validate does not use the service's bound %s:\n"+
				"the bounds are duplicated because config cannot import service, and "+
				"duplication with nothing comparing it is how a limit stops being "+
				"enforced in one place", want)
		}
	}

	// And the config must actually CHECK them, not merely mention the numbers.
	if !strings.Contains(string(cfgSrc), "PAYOUT_LAG_DAYS must be between") {
		t.Error("config.Validate does not range-check the payout lag")
	}
	if !strings.Contains(string(cfgSrc), "PayoutLagDays") {
		t.Error("config has no PayoutLagDays field; the env var cannot reach anything")
	}
}

func valueAfter(src, prefix string) int {
	rest := src[strings.Index(src, prefix)+len(prefix):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	n, _ := strconv.Atoi(rest[:end])
	return n
}
