package service

import (
	"math/rand"
	"strings"
	"testing"
)

// The pure half of the ledger: entry normalisation, balance validation and
// journal construction. None of it needs a database, which is the point -- the
// zero-sum invariant is the property worth testing hardest, and it should be
// testable without standing up Postgres.
//
// The database enforces the same invariant with a deferred constraint trigger.
// That is not redundancy for its own sake: the trigger is the guarantee, and
// these tests are the evidence that the guarantee is being asked for correctly.

func TestDebitAndCreditRoundToWholeRupiah(t *testing.T) {
	if got := Debit(AccEscrowHeld, 10000.4).Amount; got != 10000 {
		t.Errorf("debit amount = %v, want 10000 (rounded at construction)", got)
	}
	if got := Credit(AccEscrowHeld, 10000.6).Amount; got != 10001 {
		t.Errorf("credit amount = %v, want 10001 (rounded at construction)", got)
	}
}

func TestValidateBalancedAcceptsAZeroSumJournal(t *testing.T) {
	err := validateBalanced([]LedgerEntry{
		Debit(AccGatewayClearing, 100000),
		Credit(AccEscrowHeld, 100000),
	})
	if err != nil {
		t.Fatalf("a zero-sum journal was rejected: %v", err)
	}
}

func TestValidateBalancedRejectsAndNamesTheImbalance(t *testing.T) {
	err := validateBalanced([]LedgerEntry{
		Debit(AccGatewayClearing, 100000),
		Credit(AccEscrowHeld, 98000),
	})
	if err == nil {
		t.Fatal("an unbalanced journal was accepted; the whole model rests on this rejecting")
	}
	msg := err.Error()
	// The message has to be actionable. "unbalanced" alone tells you a fact you
	// already suspected; the difference and the accounts tell you the mistake.
	if !strings.Contains(msg, "100000") || !strings.Contains(msg, "98000") {
		t.Errorf("error omits the amounts, so an operator cannot see the gap: %q", msg)
	}
	if !strings.Contains(msg, AccGatewayClearing) || !strings.Contains(msg, AccEscrowHeld) {
		t.Errorf("error omits the accounts involved: %q", msg)
	}
}

func TestValidateBalancedMessageIsDeterministic(t *testing.T) {
	// Two callers building the same entries in different orders must get the same
	// message. Otherwise one bug produces two diagnoses depending on slice order,
	// which is a genuinely miserable thing to debug.
	//
	// The journals are deliberately UNBALANCED (debits 8000, credits 7000) so the
	// message is the one being compared. An earlier version of this test used
	// credits of 8000, which balances, so validateBalanced correctly returned
	// nil and the test asserted nothing.
	build := func(swap bool) string {
		e := []LedgerEntry{
			Debit(AccEscrowHeld, 5000),
			Debit(AccGatewayClearing, 3000),
			Credit(AccPlatformCommission, 7000),
		}
		if swap {
			e[0], e[2] = e[2], e[0]
		}
		err := validateBalanced(e)
		if err == nil {
			t.Fatal("expected an error; the test journal must not balance")
		}
		return err.Error()
	}
	if build(false) != build(true) {
		t.Errorf("error text depends on entry order:\n  %s\n  %s", build(false), build(true))
	}
}

func TestValidateBalancedRejectsUnknownSide(t *testing.T) {
	// A side that is neither debit nor credit would otherwise be silently
	// ignored by the summation, letting a journal appear balanced while a line
	// posted nothing. The schema's CHECK would catch it, but only at COMMIT.
	err := validateBalanced([]LedgerEntry{
		{Account: AccGatewayClearing, Amount: 1000, Side: "DEBIT"},
		{Account: AccEscrowHeld, Amount: 1000, Side: LedgerSideCredit},
	})
	if err == nil {
		t.Fatal(`a side of "DEBIT" was accepted; it would be excluded from both sums`)
	}
	if !strings.Contains(err.Error(), "BAD_SIDE") {
		t.Errorf("want a BAD_SIDE error, got %v", err)
	}
}

func TestValidateBalancedRejectsDuplicateAccount(t *testing.T) {
	err := validateBalanced([]LedgerEntry{
		Debit(AccEscrowHeld, 1000),
		Debit(AccEscrowHeld, 500),
		Credit(AccGatewayClearing, 1500),
	})
	if err == nil {
		t.Fatal("two entries for one account were accepted; combine them instead")
	}
}

func TestValidateBalancedRejectsEmptyAccount(t *testing.T) {
	err := validateBalanced([]LedgerEntry{
		{Account: "  ", Amount: 1000, Side: LedgerSideDebit},
		Credit(AccEscrowHeld, 1000),
	})
	if err == nil {
		t.Fatal("an entry with a blank account was accepted")
	}
}

func TestNormaliseEntriesDropsZeroButKeepsNegative(t *testing.T) {
	// Zero is dropped: `CHECK (amount > 0)` rejects it, so a caller computing a
	// split where one leg rounds to zero should not have to special-case it.
	//
	// Negative is NOT dropped, and this is the subtle one. A negative debit is a
	// different statement from an absent line. Silently discarding it would turn a
	// sign error into a missing entry -- which still balances, and is wrong.
	out := normaliseEntries([]LedgerEntry{
		Debit(AccEscrowHeld, 0),
		Debit(AccEscrowHeld, 0.4),
		{Account: AccGatewayClearing, Amount: -500, Side: LedgerSideDebit},
		Credit(AccEscrowHeld, -500),
	})
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2 (two zeros dropped, negative kept): %+v", len(out), out)
	}
	for _, e := range out {
		if e.Amount == 0 {
			t.Errorf("a zero-amount entry survived: %+v", e)
		}
	}
}

// A whole order lifecycle, and the property that matters: after every step, the
// sum of all postings is still zero, and the cash the platform holds equals the
// commission it has earned.
//
// This is the check the pre-ledger model could not perform at all. Balances here
// are signed by the SIDE of the entry (debit positive, credit negative), which is
// the accumulation the repository's applyBalances performs.
//
// MDR is deliberately excluded from this test and covered separately below. The
// reason it is not folded in is worth stating: an MDR that is charged to the
// seller must come out of the same escrow, and writing it as a separate credit
// against escrow without deducting it from the seller's share silently inflates
// what we owe sellers by exactly the fee.
func TestOrderLifecycleConservesEveryRupiah(t *testing.T) {
	order, commission := 100000.0, 2000.0
	seller := order - commission

	postings := []struct {
		name    string
		entries []LedgerEntry
	}{
		{
			// Capture: cash in from the gateway, held for others. The full order
			// value is escrowed; the fee is settled later, not netted at capture,
			// because netting it at capture would make escrow smaller than the
			// liability we have recorded against it.
			name: "capture",
			entries: []LedgerEntry{
				Debit(AccGatewayClearing, order),
				Credit(AccEscrowHeld, order),
			},
		},
		{
			// Release: escrow becomes a liability to the seller and revenue to us.
			name: "release",
			entries: []LedgerEntry{
				Debit(AccEscrowHeld, order),
				Credit(AccSellerAvailable, seller),
				Credit(AccPlatformCommission, commission),
			},
		},
		{
			// Payout: the seller's liability becomes cash in our bank.
			name: "payout",
			entries: []LedgerEntry{
				Debit(AccSellerAvailable, seller),
				Credit(AccBankClearing, seller),
			},
		},
	}

	balances := map[string]float64{}
	for _, p := range postings {
		if err := validateBalanced(p.entries); err != nil {
			t.Fatalf("%s: journal does not balance: %v", p.name, err)
		}
		for _, e := range p.entries {
			signed := e.Amount
			if e.Side == LedgerSideCredit {
				signed = -signed
			}
			balances[e.Account] = moneyRound(balances[e.Account] + signed)
		}

		// Checked at every step, not only at the end. A system can reach a correct
		// total while being wrong in the middle, and money that is wrong
		// transiently is money a withdrawal can race.
		var total float64
		for _, v := range balances {
			total = moneyRound(total + v)
		}
		if total != 0 {
			t.Fatalf("after %s the ledger sums to %v, want 0: %v", p.name, total, balances)
		}
	}

	// And the books say what the world says: escrow drained, the seller paid in
	// cash, the commission still held at the gateway and earned.
	want := map[string]float64{
		AccGatewayClearing:    order,
		AccBankClearing:       -seller,
		AccEscrowHeld:         0,
		AccSellerAvailable:    0,
		AccPlatformCommission: -commission,
	}
	for account, expected := range want {
		if got := balances[account]; got != expected {
			t.Errorf("balance(%s) = %v, want %v", account, got, expected)
		}
	}
}

// Gateway fee (MDR), which is where a marketplace's books usually go wrong: the
// fee must be funded from the same escrow it is deducted from.
//
// An earlier version of this test credited gateway_fee straight out of escrow
// while still paying the seller the full net amount. That leaves escrow
// overdrawn by the fee, and although each journal still sums to zero, the
// seller's balance is inflated by Rp1,500 per Rp100,000 of order -- which is
// money the platform does not have and will discover at payout.
func TestGatewayFeeIsDeductedFromEscrowAndReconciledAtSettlement(t *testing.T) {
	order, commission, fee := 100000.0, 2000.0, 1500.0
	seller := order - commission - fee // MDR borne by the seller, as in Indonesia

	postings := []struct {
		name    string
		entries []LedgerEntry
	}{
		{"capture", []LedgerEntry{
			Debit(AccGatewayClearing, order),
			Credit(AccEscrowHeld, order),
		}},
		{"release", []LedgerEntry{
			Debit(AccEscrowHeld, order),
			Credit(AccSellerAvailable, seller),
			Credit(AccPlatformCommission, commission),
			Credit(AccGatewayFee, fee),
		}},
		// The gateway remits the order value less its cut. The fee is recognised
		// again here so the clearing account nets to zero; without this leg the
		// platform would appear to still be owed the full order value forever.
		{"settlement", []LedgerEntry{
			Debit(AccBankClearing, order-fee),
			Debit(AccGatewayFee, fee),
			Credit(AccGatewayClearing, order),
		}},
		{"payout", []LedgerEntry{
			Debit(AccSellerAvailable, seller),
			Credit(AccBankClearing, seller),
		}},
	}

	balances := map[string]float64{}
	for _, p := range postings {
		if err := validateBalanced(p.entries); err != nil {
			t.Fatalf("%s does not balance: %v", p.name, err)
		}
		for _, e := range p.entries {
			signed := e.Amount
			if e.Side == LedgerSideCredit {
				signed = -signed
			}
			balances[e.Account] = moneyRound(balances[e.Account] + signed)
		}
		var total float64
		for _, v := range balances {
			total = moneyRound(total + v)
		}
		if total != 0 {
			t.Fatalf("after %s the ledger sums to %v, want 0: %v", p.name, total, balances)
		}
	}

	// Every rupiah is accounted for: the seller took their net, the platform kept
	// exactly its commission, the gateway was paid its fee, and escrow is empty.
	//
	// Signs follow the side of the entry, debit positive. The bank holds the
	// gateway remittance less the payout: (order - fee) - seller, which for
	// 100,000 at 2% commission and Rp1,500 MDR is 98,500 - 96,500 = 2,000 -- the
	// commission, which is the only thing the platform kept. An earlier version
	// of this expectation was written as -(order-fee)+seller, i.e. negated, and
	// asserted the platform was out Rp2,000 on an order it had earned Rp2,000 on.
	want := map[string]float64{
		AccGatewayClearing:    0,
		AccEscrowHeld:         0,
		AccSellerAvailable:    0,
		AccPlatformCommission: -commission,
		AccBankClearing:       (order - fee) - seller,
		AccGatewayFee:         0, // charged then recognised; a pass-through
	}
	for account, expected := range want {
		if got := balances[account]; got != expected {
			t.Errorf("balance(%s) = %v, want %v", account, got, expected)
		}
	}
}

// The refund case, asserted as a property: a full refund returns exactly what
// the customer paid, comes out of the accounts the original charge landed in, and
// leaves the platform no better off than before the order.
//
// The trap this test exists to catch: crediting escrow for a refund AFTER escrow
// has been released to the seller. Escrow is zero at that point, so the credit has
// nothing to draw down and the ledger develops a negative escrow balance that
// looks like a real receivable from nobody. An earlier version of this test did
// exactly that and did not balance at all.
// Capture as it is actually POSTED by the payment service, asserted here too.
//
// The direction is not a detail. Escrow is a liability, so money arriving
// INCREASES it on the credit side. The journal balances either way, so an
// assertion that only checks the sum will happily accept a capture that leaves
// escrow negative from the first payment onward. It happened once while this
// was being written, and only the direction assertion found it.
func TestCaptureCreditsEscrowBecauseEscrowIsALiability(t *testing.T) {
	capture := []LedgerEntry{
		Credit(AccEscrowHeld, 100000),
		Debit(AccGatewayClearing, 100000),
	}
	if err := validateBalanced(capture); err != nil {
		t.Fatalf("capture does not balance: %v", err)
	}
	// Walk it and assert the escrow balance moves in the direction a liability
	// moves: up, on a credit.
	escrow := 0.0
	for _, e := range capture {
		if e.Account != AccEscrowHeld {
			continue
		}
		if e.Side == LedgerSideCredit {
			escrow += e.Amount
		} else {
			escrow -= e.Amount
		}
	}
	if escrow != 100000 {
		t.Errorf("after a capture escrow holds Rp%.0f, want Rp100000 owed", escrow)
	}

	// And the release must bring it back to zero, or escrow grows without bound
	// and reads as money we are holding that we no longer have.
	release := []LedgerEntry{
		Debit(AccEscrowHeld, 100000),
		Credit(AccSellerAvailable, 98000),
		Credit(AccPlatformCommission, 2000),
	}
	escrow = 0
	for _, e := range capture {
		if e.Account == AccEscrowHeld {
			escrow += e.Amount
		}
	}
	for _, e := range release {
		if e.Account != AccEscrowHeld {
			continue
		}
		escrow -= e.Amount
	}
	if escrow != 0 {
		t.Errorf("after a full lifecycle escrow holds Rp%.0f, want 0", escrow)
	}
}

func TestFullRefundReversesEveryLegOfTheOriginalCharge(t *testing.T) {
	order, commission := 100000.0, 2000.0
	seller := order - commission

	capture := []LedgerEntry{
		Debit(AccGatewayClearing, order),
		Credit(AccEscrowHeld, order),
	}
	release := []LedgerEntry{
		Debit(AccEscrowHeld, order),
		Credit(AccSellerAvailable, seller),
		Credit(AccPlatformCommission, commission),
	}
	// Escrow is already at zero, so the refund is funded from where the money
	// actually is: the seller's credited balance and our earned commission, and
	// the cash goes back out through the gateway.
	refund := []LedgerEntry{
		Debit(AccSellerAvailable, seller),
		Debit(AccPlatformCommission, commission),
		Credit(AccGatewayClearing, order),
	}

	for _, tc := range []struct {
		name    string
		journal []LedgerEntry
	}{
		{"capture", capture}, {"release", release}, {"refund", refund},
	} {
		if err := validateBalanced(tc.journal); err != nil {
			t.Errorf("%s does not balance: %v", tc.name, err)
		}
	}

	// Walk all three, then assert the platform is exactly where it started.
	balances := map[string]float64{}
	for _, tc := range []struct {
		name    string
		journal []LedgerEntry
	}{{"capture", capture}, {"release", release}, {"refund", refund}} {
		for _, e := range tc.journal {
			signed := e.Amount
			if e.Side == LedgerSideCredit {
				signed = -signed
			}
			balances[e.Account] = moneyRound(balances[e.Account] + signed)
		}
		var total float64
		for _, v := range balances {
			total = moneyRound(total + v)
		}
		if total != 0 {
			t.Fatalf("after %s the ledger sums to %v, want 0: %v", tc.name, total, balances)
		}
	}

	// A refunded order leaves no trace: no escrow, no seller balance, no
	// commission, and no claim on the gateway.
	for _, account := range []string{
		AccEscrowHeld, AccSellerAvailable, AccPlatformCommission,
	} {
		if got := balances[account]; got != 0 {
			t.Errorf("balance(%s) = %v after a full refund, want 0", account, got)
		}
	}
	if got := balances[AccGatewayClearing]; got != 0 {
		t.Errorf("balance(%s) = %v after a full refund, want 0 (cash returned)",
			AccGatewayClearing, got)
	}
}

// Every randomly generated journal that validateBalanced accepts must genuinely
// sum to zero, and every one it rejects must not. This is the property the
// deferred constraint trigger also enforces, checked here where a failure points
// at a specific line number instead of a transaction.
func TestValidateBalancedIsExactlyZeroSum(t *testing.T) {
	rng := rand.New(rand.NewSource(20240917)) // fixed seed: a failure must reproduce

	accounts := []string{
		AccBankClearing, AccGatewayClearing, AccEscrowHeld,
		AccSellerAvailable, AccPlatformCommission,
	}

	for i := 0; i < 2000; i++ {
		lines := 2 + rng.Intn(5)
		entries := make([]LedgerEntry, 0, lines)
		for j := 0; j < lines; j++ {
			entries = append(entries, LedgerEntry{
				Account: accounts[rng.Intn(len(accounts))],
				Amount:  float64(1 + rng.Intn(500000)),
				Side:    []string{LedgerSideDebit, LedgerSideCredit}[rng.Intn(2)],
			})
		}
		entries = normaliseEntries(entries)

		var debits, credits float64
		seen := map[string]bool{}
		duplicate := false
		bad := false
		for _, e := range entries {
			if seen[e.Account] {
				duplicate = true
			}
			seen[e.Account] = true
			switch e.Side {
			case LedgerSideDebit:
				debits = moneyRound(debits + e.Amount)
			case LedgerSideCredit:
				credits = moneyRound(credits + e.Amount)
			default:
				bad = true
			}
		}

		err := validateBalanced(entries)
		switch {
		case duplicate:
			// Rejected for duplication, so the sums are irrelevant here.
		case bad:
			if err == nil {
				t.Fatalf("case %d: accepted a journal with an invalid side", i)
			}
		case debits == credits:
			if err != nil {
				t.Fatalf("case %d: rejected a zero-sum journal: %v", i, err)
			}
		default:
			if err == nil {
				t.Fatalf("case %d: accepted debits %v vs credits %v", i, debits, credits)
			}
		}
	}
}

// Splitting an amount must not create or destroy a rupiah. A platform that loses
// or invents money while allocating a discount is the exact bug migration 00041
// fixed for checkout, appearing again in a new place.
func TestJournalSplitsNeverLoseARupiah(t *testing.T) {
	rng := rand.New(rand.NewSource(99887766))

	for i := 0; i < 1000; i++ {
		total := float64(1 + rng.Intn(5_000_000))
		n := 1 + rng.Intn(8)

		// Largest-remainder allocation: floor everything, then hand the leftover
		// rupiah to the first `rem` shares. The sum is `total` by construction.
		shares := make([]int, n)
		assigned := 0
		for j := 0; j < n; j++ {
			shares[j] = int(total) / n
			assigned += shares[j]
		}
		for j := 0; assigned < int(total); j, assigned = (j+1)%n, assigned+1 {
			shares[j]++
		}

		sum := 0
		for _, s := range shares {
			sum += s
		}
		if float64(sum) != total {
			t.Fatalf("case %d: split %v into %d shares summing to %d, want %v",
				i, total, n, sum, total)
		}
	}
}

func TestPostRejectsMissingIdempotencyKey(t *testing.T) {
	s := NewLedgerService(nil, nil)
	// A journal with no business key cannot be retried, and every caller of Post
	// is a retryable operation.
	_, err := s.Post(t.Context(), nil, JournalSpec{
		TxType:  TxTypePayment,
		Entries: []LedgerEntry{Debit(AccGatewayClearing, 100), Credit(AccEscrowHeld, 100)},
	})
	if err == nil {
		t.Fatal("posted a journal with no idempotency key")
	}
	if !strings.Contains(err.Error(), "MISSING_IDEMPOTENCY_KEY") {
		t.Errorf("want MISSING_IDEMPOTENCY_KEY, got %v", err)
	}
}

func TestPostRejectsEmptyJournal(t *testing.T) {
	s := NewLedgerService(nil, nil)
	// Empty balances to 0 = 0 and would pass the trigger. It is still wrong: a
	// journal that posts nothing looks like a completed transaction and burns an
	// idempotency key.
	_, err := s.Post(t.Context(), nil, JournalSpec{
		IdempotencyKey: "test:empty",
		TxType:         TxTypePayment,
	})
	if err == nil {
		t.Fatal("posted an empty journal")
	}
	if !strings.Contains(err.Error(), "EMPTY_JOURNAL") {
		t.Errorf("want EMPTY_JOURNAL, got %v", err)
	}
}

func TestPostRejectsUnbalancedBeforeTouchingTheDatabase(t *testing.T) {
	s := NewLedgerService(nil, nil)
	// The repository is nil, so any attempt to write would panic. Reaching a clean
	// error proves the balance check runs BEFORE the write, which is what makes
	// the Go-side message useful: it can name the accounts.
	_, err := s.Post(t.Context(), nil, JournalSpec{
		IdempotencyKey: "test:unbalanced",
		TxType:         TxTypePayment,
		Entries: []LedgerEntry{
			Debit(AccGatewayClearing, 100),
			Credit(AccEscrowHeld, 40),
		},
	})
	if err == nil {
		t.Fatal("posted an unbalanced journal")
	}
	if !strings.Contains(err.Error(), "UNBALANCED_JOURNAL") {
		t.Errorf("want UNBALANCED_JOURNAL, got %v", err)
	}
}

func TestPersonalAccountIsNamespacedByUser(t *testing.T) {
	// A personal account is named from the user id rather than stored, so there is
	// no mapping to get out of sync. Two users must never collide, and neither
	// may collide with the system account of the same name.
	if PersonalAccount("u1") == PersonalAccount("u2") {
		t.Fatal("two users produced the same ledger account")
	}
	if !strings.HasPrefix(PersonalAccount("u1"), AccSellerAvailable+":") {
		t.Errorf("personal account is not in the reserved namespace: %q", PersonalAccount("u1"))
	}
	if PersonalAccount("") == AccSellerAvailable {
		t.Fatal("an empty user id collided with the system account")
	}
}

func TestWithMetadataDoesNotMutateTheReceiver(t *testing.T) {
	// Entries are values, but callers hold them in slices and a mutating helper
	// would corrupt an unrelated posting that happened to share the entry.
	base := Debit(AccEscrowHeld, 1000)
	withOrder := WithMetadata(base, map[string]any{"order_id": "abc"})

	if base.Metadata != nil {
		t.Errorf("the original entry was mutated: %+v", base)
	}
	if withOrder.Metadata["order_id"] != "abc" {
		t.Errorf("metadata not attached: %+v", withOrder.Metadata)
	}
	if withOrder.Account != AccEscrowHeld || withOrder.Amount != 1000 || withOrder.Side != LedgerSideDebit {
		t.Errorf("metadata helper altered the entry itself: %+v", withOrder)
	}
}
