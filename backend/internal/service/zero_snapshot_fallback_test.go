package service

import (
	"strings"
	"testing"
)

// THE TRAP THIS EXISTS TO PREVENT.
//
// Migration 00042 added the commission snapshot as `NOT NULL DEFAULT 0`:
//
//	commission_rate_pct  numeric(5,2) NOT NULL DEFAULT 0
//	commission_rate_fixed numeric(14,2) NOT NULL DEFAULT 0
//
// so an intent with no snapshot and an intent priced at a genuine 0% are
// INDISTINGUISHABLE in those columns -- both read (0, 0). The codebase currently
// disagrees with itself about which one it is: the refund path's comment says "a
// promo-period order released at 0% has fee_amount = 0", i.e. that 0% is a real
// value, while two sites read (0, 0) as "not provided". Recorded in
// docs/BACKLOG.md rather than guessed at.
//
// The tempting fix is to stop treating (0, 0) as a sentinel and let it mean 0%.
// That would be WRONG, and expensively so: every intent that predates 00042 has
// (0, 0) because the columns were added with DEFAULT 0 and backfilled only for
// rows that were already released. Defaulting those to 0% means commission is
// silently never charged on them -- the platform gives away its fee on every
// legacy order, and the only symptom is revenue that is quietly lower.
//
// Falling back to the ACTIVE rate is the deliberate, documented choice
// ("strictly better than a zero fee"). These tests pin that so the wrong fix
// cannot land by accident. Closing the ambiguity properly needs a nullable
// column or an explicit `commission_snapshot_present` flag -- not a changed
// default.
func TestReleaseEscrowDoesNotTreatAnAbsentSnapshotAsZeroPercent(t *testing.T) {
	body := releaseEscrowBody(t)

	at := strings.Index(body, "if pct == 0 && fixed == 0 {")
	if at < 0 {
		t.Fatal("the snapshot-less fallback is gone from ReleaseEscrow.\n" +
			"If (0, 0) is now taken to mean 0%, every pre-00042 intent stops paying " +
			"commission. That is the wrong fix -- see docs/BACKLOG.md")
	}
	end := strings.Index(body[at:], "\n\t\t}")
	if end < 0 {
		t.Fatal("could not bound the fallback block")
	}
	fallback := body[at : at+end]

	if !strings.Contains(fallback, "ActiveFeeTx(") {
		t.Errorf("the snapshot-less fallback does not read the active rate.\n%s\n"+
			"Falling back to zero would stop charging commission on every intent whose "+
			"snapshot was never written", fallback)
	}
	// Assert the POSITIVE form, not "must not contain a literal 0". A negative check
	// on the string `pct, fixed = 0` is satisfied by `pct, fixed = cfg.Pct*0`, which
	// is the same defect wearing a hat -- and it was caught by mutation checking:
	// the obvious mutants did not even compile (`cfg` became unused), so the
	// negative assertion was never actually exercised.
	if !strings.Contains(fallback, "pct, fixed = cfg.Pct, cfg.Fixed") {
		t.Errorf("the fallback does not assign the active rate to both pct and fixed.\n%s\n"+
			"Anything else here means either zero (commission never charged on legacy "+
			"intents) or a partial rate", fallback)
	}
}

// The same rule on the refund side. It reads the active rate for an intent whose
// commission was never computed, and that is the behaviour to keep.
func TestRefundPathDoesNotTreatAnAbsentSnapshotAsZeroPercent(t *testing.T) {
	body := refundPlanBody(t)

	at := strings.Index(body, "if !intent.CommissionComputed {")
	if at < 0 {
		t.Fatal("the refund path no longer guards on CommissionComputed; update this test")
	}
	end := strings.Index(body[at:], "\n\t}")
	if end < 0 {
		t.Fatal("could not bound the refund fallback")
	}
	fallback := body[at : at+end]

	if !strings.Contains(fallback, "ActiveFeeTx(") {
		t.Errorf("the refund path does not fall back to the active rate for an intent "+
			"with no computed commission.\n%s\n"+
			"Recomputing at zero would refund a fee the platform never charged", fallback)
	}
	// Positive form for the same reason as the release test above.
	if !strings.Contains(fallback, "repository.Commission(cfg.Pct, cfg.Fixed, intent.Amount)") {
		t.Errorf("the refund fallback does not derive the legs from the active rate.\n%s", fallback)
	}
}

// refundPlanBody bounds the function that derives a refund's fee legs. Kept
// separate so a rename surfaces as a clear failure rather than a confusing one.
func refundPlanBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "payment_service.go")
	at := strings.Index(src, "func (s *PaymentService) buildRefundPlan(")
	if at < 0 {
		t.Fatal("buildRefundPlan is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}
