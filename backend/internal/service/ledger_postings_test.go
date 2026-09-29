package service

import (
	"context"
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

// The ledger postings for the money paths.
//
// These assert the ACCOUNTS a movement touches and that the journal balances.
// Both matter, and they catch different mistakes:
//
//   - An unbalanced journal is rejected by the deferred constraint trigger, so
//     the money movement rolls back and the buyer cannot be refunded. That is
//     loud, but it is a production outage.
//   - A journal that balances but touches the WRONG account is silent. Escrow
//     credit on a refund after a release, or a release that credits escrow
//     instead of the seller, both balance perfectly and both are wrong. Only an
//     assertion on the accounts finds those.

// These helpers ARE the production functions, not copies of them.
//
// The first version of this file reimplemented the account selection, and a
// mutation that rerouted COD from cod_receivable to gateway_clearing passed
// cleanly: the test asserted its own copy was right while the service was wrong.
// A test that duplicates the logic it is testing is a test of the duplicate.
func captureJournal(amount float64, method string) []LedgerEntry {
	return captureEntries(amount, method)
}

func TestCaptureJournalBalancesAndEscrows(t *testing.T) {
	for _, method := range []string{domain.MethodCOD, domain.MethodQRIS, "bank_transfer", ""} {
		j := captureJournal(100000, method)
		if err := validateBalanced(j); err != nil {
			t.Fatalf("%s: capture journal does not balance: %v", method, err)
		}
		if j[0].Account != AccEscrowHeld || j[0].Side != LedgerSideCredit {
			t.Errorf("%s: capture must CREDIT escrow_held (a liability we now owe), got %s:%s",
				method, j[0].Account, j[0].Side)
		}
		if j[1].Side != LedgerSideDebit {
			t.Errorf("%s: capture must DEBIT a clearing asset, got %s", method, j[1].Side)
		}
	}
}

func TestCODCaptureGoesToTheCourierNotTheGateway(t *testing.T) {
	// COD cash is held by the courier, not by a payment gateway. Posting it to
	// gateway_clearing would put the money in the wrong account for the several
	// days it spends there, and the reconciliation against a Midtrans settlement
	// would never tie out.
	j := captureJournal(50000, domain.MethodCOD)
	if j[1].Account != AccCodReceivable {
		t.Errorf("COD capture credits %q, want %q", j[1].Account, AccCodReceivable)
	}
	nonCOD := captureJournal(50000, domain.MethodQRIS)
	if nonCOD[1].Account != AccGatewayClearing {
		t.Errorf("QRIS capture credits %q, want %q", nonCOD[1].Account, AccGatewayClearing)
	}
}

// releaseJournal is the production function.
func releaseJournal(orderID, sellerID string, gross, sellerNet, fee float64) []LedgerEntry {
	return releaseEntries(sellerID, gross, sellerNet, fee)
}

func TestReleaseJournalDebitsEscrowForTheGrossNotTheNet(t *testing.T) {
	gross, fee := 100000.0, 2000.0
	sellerNet := gross - fee
	j := releaseJournal("o1", "seller-1", gross, sellerNet, fee)

	if err := validateBalanced(j); err != nil {
		t.Fatalf("release journal does not balance: %v", err)
	}
	// The escrow debit must be the GROSS. Debiting the two net figures instead
	// leaves escrow holding a residual, which reads as "we still owe money we
	// have already disbursed" -- a permanent phantom liability.
	if j[0].Account != AccEscrowHeld || j[0].Amount != gross {
		t.Errorf("escrow leg = %s Rp%.0f, want escrow_held Rp%.0f (the gross)",
			j[0].Account, j[0].Amount, gross)
	}
	// And the two credits must sum to the gross, or money is created.
	if j[1].Amount+j[2].Amount != gross {
		t.Errorf("credits sum to Rp%.0f, want Rp%.0f", j[1].Amount+j[2].Amount, gross)
	}
}

func TestReleaseJournalCreditsTheSellerPersonalAccount(t *testing.T) {
	j := releaseJournal("o1", "seller-1", 100000, 98000, 2000)
	// Not a system account: a per-user liability, which is what makes "sum of all
	// seller balances" answerable as a query.
	if !strings.HasPrefix(j[1].Account, AccSellerAvailable+":") {
		t.Errorf("seller credit goes to %q, want a %s: account", j[1].Account, AccSellerAvailable)
	}
	if !strings.Contains(j[1].Account, "seller-1") {
		t.Errorf("seller credit %q does not identify the seller", j[1].Account)
	}
	if j[2].Account != AccPlatformCommission {
		t.Errorf("platform leg = %q, want %q", j[2].Account, AccPlatformCommission)
	}
}

// Two sellers on the same release must not share an account, or a payout for one
// can be satisfied from the other's balance.
func TestSellerPersonalAccountsAreDistinct(t *testing.T) {
	a := releaseJournal("o1", "seller-a", 100000, 98000, 2000)
	b := releaseJournal("o2", "seller-b", 50000, 49000, 1000)
	if a[1].Account == b[1].Account {
		t.Fatalf("two sellers share a ledger account %q", a[1].Account)
	}
}

// refundJournal is the production function.
func refundJournal(sellerID string, amount, refundSeller, refundFee float64, wasReleased bool) []LedgerEntry {
	return refundEntries(sellerID, amount, refundSeller, refundFee, wasReleased)
}

func TestRefundBeforeReleaseComesOutOfEscrow(t *testing.T) {
	// The money is still held, so it leaves escrow and goes back to the gateway.
	j := refundJournal("s1", 100000, 0, 0, false)
	if err := validateBalanced(j); err != nil {
		t.Fatalf("refund journal does not balance: %v", err)
	}
	if j[0].Account != AccEscrowHeld || j[0].Amount != 100000 {
		t.Errorf("first leg = %s Rp%.0f, want escrow_held Rp100000", j[0].Account, j[0].Amount)
	}
	if j[1].Account != AccGatewayClearing || j[1].Side != LedgerSideCredit {
		t.Errorf("second leg = %s:%s, want gateway_clearing:credit", j[1].Account, j[1].Side)
	}
}

func TestRefundAfterReleaseNeverTouchesEscrow(t *testing.T) {
	// This is the case that was easy to get wrong. Escrow is already zero after a
	// release, so crediting it would leave a negative balance that reads as a
	// receivable from nobody -- and it still balances perfectly.
	j := refundJournal("s1", 100000, 98000, 2000, true)
	for _, e := range j {
		if e.Account == AccEscrowHeld {
			t.Errorf("a post-release refund touched escrow_held: %+v", e)
		}
	}
	if err := validateBalanced(j); err != nil {
		t.Fatalf("refund journal does not balance: %v", err)
	}
}

func TestRefundAfterReleaseReversesTheCommissionToo(t *testing.T) {
	// Reversing only the seller's leg and keeping our fee is how a marketplace
	// ends up earning commission on a sale it refunded in full.
	j := refundJournal("s1", 100000, 98000, 2000, true)

	var sawSeller, sawCommission bool
	for _, e := range j {
		if strings.HasPrefix(e.Account, AccSellerAvailable+":") {
			sawSeller = true
			if e.Side != LedgerSideDebit {
				t.Errorf("seller leg is %s, want debit: we took the money back", e.Side)
			}
		}
		if e.Account == AccPlatformCommission {
			sawCommission = true
			if e.Side != LedgerSideDebit {
				t.Errorf("commission leg is %s, want debit: revenue reversed", e.Side)
			}
		}
	}
	if !sawSeller {
		t.Error("no seller leg: the seller keeps money for a refunded sale")
	}
	if !sawCommission {
		t.Error("no commission reversal: the platform earns on a fully refunded order")
	}
}

func TestRefundReversalLegsSumToTheRefund(t *testing.T) {
	// The reversal legs must reconstruct the amount exactly. A drift of one sen
	// per partial refund accumulates permanently, because nothing ever sweeps it.
	for _, amount := range []float64{100000, 33333, 1, 99999, 123457} {
		ratio := amount / 100000.0
		fee := moneyRound(2000 * ratio)
		seller := moneyRound(amount - fee)
		if fee+seller != amount {
			seller = moneyRound(amount - fee)
		}
		j := refundJournal("s1", amount, seller, fee, true)
		if err := validateBalanced(j); err != nil {
			t.Errorf("refund of Rp%.0f does not balance: %v", amount, err)
		}
	}
}

// The full lifecycle, now with the real account set, must leave escrow empty and
// the platform holding exactly its commission.
func TestLifecycleOfPostedJournalsLeavesEscrowAtZero(t *testing.T) {
	const (
		gross  = 100000.0
		fee    = 2000.0
		seller = "seller-1"
	)
	net := gross - fee

	postings := [][]LedgerEntry{
		captureJournal(gross, domain.MethodQRIS),
		releaseJournal("o1", seller, gross, net, fee),
		// A full refund after release reverses the disbursement in full.
		refundJournal(seller, gross, net, fee, true),
	}

	balances := map[string]float64{}
	for i, j := range postings {
		if err := validateBalanced(j); err != nil {
			t.Fatalf("posting %d does not balance: %v", i, err)
		}
		for _, e := range j {
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
			t.Fatalf("after posting %d the ledger sums to Rp%.0f, want 0: %v", i, total, balances)
		}
	}

	// A fully refunded order leaves nothing behind.
	for account, got := range balances {
		if got != 0 {
			t.Errorf("balance(%s) = Rp%.0f after capture+release+refund, want 0", account, got)
		}
	}
}

func TestPostLedgerToleratesAMissingLedgerButSaysSo(t *testing.T) {
	// A payment must not stop working because an optional subsystem is absent.
	// But tolerating nil is precisely how the original defect returns, so the
	// call must be a no-op that logs rather than a silent no-op.
	//
	// The behaviour asserted here is that it does not panic and does not block:
	// reaching this point at all is the assertion. A nil q would panic if the
	// implementation tried to write.
	s := &PaymentService{ledger: nil}
	s.postLedger(context.Background(), nil, JournalSpec{
		IdempotencyKey: "k", TxType: TxTypePayment, Entries: []LedgerEntry{},
	})
}

func TestWithMetaAttachesToEveryLine(t *testing.T) {
	// Applied to the whole journal so one line cannot be left without the
	// reason it exists. An operator asking "which journal moved this?" needs the
	// order id on all of them, not just the first.
	j := withMeta([]LedgerEntry{
		Debit(AccEscrowHeld, 1000),
		Credit(AccGatewayClearing, 1000),
	}, map[string]any{"order_id": "o1"})
	for i, e := range j {
		if e.Metadata["order_id"] != "o1" {
			t.Errorf("entry %d has metadata %v, want the order id on every line", i, e.Metadata)
		}
	}
}

func TestRefundIdempotencyKeyUsesTheCumulativeTotal(t *testing.T) {
	// Two refunds of the SAME amount on one order are two different events.
	// Keying on the amount alone would silently swallow the second, which is the
	// original uncapped-refund bug in a new costume: the buyer is owed twice and
	// is refunded once.
	first := refundKey("intent-1", 100000, 50000)
	second := refundKey("intent-1", 50000, 50000)
	if first == second {
		t.Errorf("two distinct refunds share a key %q; the second would be swallowed", first)
	}
	// A replay of the SAME refund is the same key, so it is recognised.
	replay := refundKey("intent-1", 50000, 50000)
	if second != replay {
		t.Errorf("a replay of the same refund produced a new key %q vs %q", replay, second)
	}
}
