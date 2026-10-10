package service

import (
	"strings"
	"testing"
)

// walletCaptureBlock returns the source of the wallet branch of InitiatePayment --
// from `if in.Method == "wallet"` to the start of the COD branch. Scoped rather than
// searching the whole file, because the gateway and COD paths legitimately contain
// the same call and a file-wide assertion could be satisfied by either of them.
func walletCaptureBlock(t *testing.T) string {
	t.Helper()
	src := readSource(t, "payment_service.go")
	start := strings.Index(src, `if in.Method == "wallet" {`)
	if start < 0 {
		t.Fatal("the wallet branch is gone from InitiatePayment; update this test")
	}
	end := strings.Index(src[start:], `if in.Method == "cod" {`)
	if end < 0 {
		t.Fatal("no COD branch after the wallet branch; update this test")
	}
	return src[start : start+end]
}

// THE DEFECT.
//
// The wallet path debited the buyer's balance, then wrote the order status with
// `tx.SetStatus`, which is:
//
//	UPDATE orders SET status = $2 ... WHERE id = $1 AND status <> $2
//
// `status <> $2` only refuses ALREADY-paid. It does not care what the order
// currently is, so `cancelled -> paid` succeeds.
//
// The check at the top of InitiatePayment (`order.Status != domain.OrderPending`)
// runs BEFORE the transaction begins, so the sweeper or the buyer can cancel in the
// window between that read and this commit. In that window the debit has already
// happened, and the unguarded write resurrects a cancelled order as paid: money
// taken for goods nobody will ship.
//
// COD and the gateway capture path both use the guarded form. The wallet path was
// the one that took money and did not.
func TestWalletCaptureGuardsTheOrderStatusTransition(t *testing.T) {
	wallet := walletCaptureBlock(t)

	if strings.Contains(wallet, "tx.SetStatus(ctx,") {
		t.Errorf("the wallet capture path writes the order status unguarded.\n%s\n"+
			"SetStatus is `WHERE status <> $2`, so it overwrites cancelled. The read that "+
			"checks for pending happens before the transaction opens, so it cannot stop a "+
			"cancel that lands in between -- and by then the wallet has been debited", wallet)
	}
	if !strings.Contains(wallet,
		"tx.SetStatusGuarded(ctx, order.ID, domain.OrderPending, domain.OrderPaid)") {
		t.Errorf("the wallet capture path does not guard pending -> paid.\n%s\n"+
			"Without `WHERE status = 'pending'` the database is not the arbiter and the "+
			"debit and the status can disagree", wallet)
	}
}

// The guard has to come AFTER the debit in the same transaction, not before it and
// not in a separate transaction. Either would leave the two able to disagree: a
// guard that runs first is still only a check, and one that runs outside the
// transaction commits independently of the money.
func TestTheWalletDebitAndTheStatusGuardShareOneTransaction(t *testing.T) {
	wallet := walletCaptureBlock(t)

	begin := strings.Index(wallet, "s.orders.Begin(ctx)")
	debit := strings.Index(wallet, "WalletTxOn(ctx, tx.PgTx()")
	guard := strings.Index(wallet, "SetStatusGuarded(ctx, order.ID,")
	commit := strings.Index(wallet, "tx.Commit(ctx)")

	for name, at := range map[string]int{
		"Begin": begin, "the wallet debit": debit,
		"the status guard": guard, "Commit": commit,
	} {
		if at < 0 {
			t.Fatalf("%s is missing from the wallet branch; update this test", name)
		}
	}
	if !(begin < debit && debit < guard && guard < commit) {
		t.Errorf("wallet capture order is Begin(%d) debit(%d) guard(%d) Commit(%d).\n"+
			"All four have to be in that order: the debit and the status move together or "+
			"neither does", begin, debit, guard, commit)
	}
	// One transaction on this path, not two. A second Commit would mean the money and
	// the status are committed separately and can disagree.
	if strings.Count(wallet, "tx.Commit(ctx)") != 1 {
		t.Errorf("the wallet branch commits %d times, want exactly 1", strings.Count(wallet, "tx.Commit(ctx)"))
	}
}

// The event written alongside it must describe the transition that actually
// happened. It hardcodes FromStatus: pending, which is only truthful while the
// guarded write above guarantees the order really was pending.
func TestTheWalletPaidEventClaimsPendingAsItsFromState(t *testing.T) {
	wallet := walletCaptureBlock(t)

	at := strings.Index(wallet, "tx.AddEvent(ctx,")
	if at < 0 {
		t.Fatal("no AddEvent in the wallet branch; update this test")
	}
	end := strings.Index(wallet[at:], "})")
	if end < 0 {
		t.Fatal("could not bound the AddEvent call")
	}
	event := wallet[at : at+end]
	if !strings.Contains(event, "FromStatus: domain.OrderPending") {
		t.Errorf("the wallet capture event does not record pending as its from-state.\n%s\n"+
			"A `paid` event claiming to come from somewhere other than pending is an audit "+
			"trail that contradicts the transition", event)
	}
}
