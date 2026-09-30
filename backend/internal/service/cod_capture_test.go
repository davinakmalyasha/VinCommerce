package service

import (
	"strings"
	"testing"
)

// COD cash was never booked.
//
// `onPaid` posts a capture journal and `captureIntentOnly` did not, and `onPaid` is
// reachable only from `HandleWebhook` -- so only gateway payments were ever
// recorded. COD capture happens through a different door, at delivery. So for a COD
// order the buyer paid cash to a courier, `escrow_held` was never debited, and
// `cod_receivable` sat at zero forever.
//
// `TestCODCaptureGoesToTheCourierNotTheGateway` existed and passed, and its comment
// claimed it pinned this. It pinned the HELPER: `captureEntries` did route COD to
// `cod_receivable` correctly, and nothing ever called it with a COD method. A test
// of the classifier, not of the wiring -- the fifth instance of that pattern in this
// workstream.
//
// These assert the CALL, which is the half that was missing.

// captureIntentOnly must post a journal, and post it through the STRICT helper.
//
// `postLedger` swallows a posting failure, which is correct for a movement that has
// already committed and is why this omission was invisible: the money moved, the
// journal silently did not, and nothing anywhere compared the two.
func TestCODCapturePostsItsJournalThroughTheStrictPath(t *testing.T) {
	body := functionSource(t, "payment_service.go", "captureIntentOnly")

	if !strings.Contains(body, "postLedgerStrict(") {
		t.Errorf("COD capture posts no journal, or posts it through the swallowing "+
			"helper:\n%s\n"+
			"the buyer paid cash to a courier and the books recorded nothing, so "+
			"escrow_held was never debited and cod_receivable never moved", body)
	}
	if strings.Contains(body, "s.postLedger(ctx,") {
		t.Errorf("COD capture uses the swallowing postLedger:\n%s\n"+
			"a failed posting must stop the capture, not leave the money collected "+
			"with nothing recording it", body)
	}
	if !strings.Contains(body, "captureEntries(intent.Amount, intent.Method)") {
		t.Errorf("COD capture does not use captureEntries:\n%s\n"+
			"that helper is what routes COD to cod_receivable rather than "+
			"gateway_clearing, because the courier holds that cash for several days", body)
	}
	// The method must come from the INTENT, not a literal. Passing "cod" as a
	// constant would make the journal look right while being wrong for any future
	// method that is not a gateway.
	if strings.Contains(body, `captureEntries(intent.Amount, "cod")`) {
		t.Errorf("the capture method is hardcoded to cod:\n%s\n"+
			"the intent's own method is the source of truth, and hardcoding it would "+
			"misroute any method added later", body)
	}
	// And the journal must be inside the transaction, before the commit.
	postAt, commitAt := strings.Index(body, "postLedgerStrict("), strings.Index(body, "tx.Commit(ctx)")
	if postAt < 0 || commitAt < 0 || postAt > commitAt {
		t.Errorf("the capture journal is posted after the commit:\n%s\n"+
			"a journal that lands in a different transaction from the money movement "+
			"is exactly the window this codebase keeps getting wrong", body)
	}
}

// The idempotency key must match the gateway path's, or the two doors cannot both
// be safe.
//
// Both use `capture:<intent id>`. That is what makes a COD capture idempotent: a
// retried delivery confirmation returns the original journal rather than posting
// the escrow debit twice. Two different keys would let the same intent be captured
// twice, and `SetIntentStatusGuardedTx` would stop it -- but only for the status,
// not for a journal posted by a path that does not check.
func TestTheCaptureJournalIsIdempotentUnderTheSameKeyAsTheGatewayPath(t *testing.T) {
	cod := functionSource(t, "payment_service.go", "captureIntentOnly")
	gateway := functionSource(t, "payment_service.go", "onPaid")

	if idempotencyKeyOf(cod) != idempotencyKeyOf(gateway) {
		t.Errorf("COD and gateway capture use different idempotency keys:\n"+
			"  cod:     %q\n  gateway: %q\n"+
			"the same business event -- funds captured against an intent -- must have "+
			"one key, or a retried confirmation can post it twice", idempotencyKeyOf(cod), idempotencyKeyOf(gateway))
	}
	if idempotencyKeyOf(cod) == "" {
		t.Errorf("COD capture has no idempotency key:\n%s\n"+
			"a retried delivery confirmation would post the escrow debit again", cod)
	}
}

// The account the cash lands in, asserted end to end rather than on the helper.
//
// `cod_receivable` is the whole reason the helper branches: the courier holds that
// money on the platform's behalf for several days, so posting it to
// `gateway_clearing` would put it in the wrong place for the length of a delivery
// window. A test of the helper is a test of the branch; this asserts that a COD
// capture actually reaches it.
func TestACODCaptureReachesTheCourierAccountAndNotTheGateway(t *testing.T) {
	cod := functionSource(t, "payment_service.go", "captureIntentOnly")
	if !strings.Contains(cod, "captureEntries(intent.Amount, intent.Method)") {
		t.Fatalf("COD capture does not route through captureEntries:\n%s", cod)
	}
	// And the journal must carry the intent's method as metadata, so an auditor
	// reading the entry can tell which door the money came in by.
	if !strings.Contains(cod, `"method": intent.Method`) {
		t.Errorf("the journal does not carry the intent's method:\n%s\n"+
			"the routing decision is made from it, and omitting it would send every "+
			"capture to the gateway clearing account", cod)
	}

	// And the helper must still route it, since a mutation could break either end.
	entries := captureEntries(100000, "cod")
	if !hasAccount(entries, AccCodReceivable) {
		t.Errorf("a COD capture does not credit cod_receivable: %v", accountsOf(entries))
	}
	if hasAccount(entries, AccGatewayClearing) {
		t.Errorf("a COD capture credits gateway_clearing: %v\n"+
			"the courier holds that cash, not a payment gateway, and for several days",
			accountsOf(entries))
	}
}

func hasAccount(entries []LedgerEntry, account string) bool {
	for _, e := range entries {
		if e.Account == account {
			return true
		}
	}
	return false
}

func accountsOf(entries []LedgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Account)
	}
	return out
}
