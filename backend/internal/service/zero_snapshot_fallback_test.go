package service

import (
	"strings"
	"testing"
)

// THE TRAP THIS EXISTS TO PREVENT.
//
// Migration 00042 added the commission snapshot as `NOT NULL DEFAULT 0`, so an
// intent with no snapshot and an intent priced at a genuine 0% were
// indistinguishable -- both read (0, 0). The tempting fix is to stop treating
// (0, 0) as a sentinel and let it mean 0%. That would be WRONG, and expensively
// so: every intent predating 00042 has (0, 0) because the columns were added with
// that default and backfilled only for rows already released. Defaulting those to
// 0% means commission is silently never charged on them, and the only symptom is
// revenue that is quietly lower.
//
// Migration 00052 resolves the ambiguity properly with
// `commission_snapshot_present`, so these tests now guard the NEW predicate. The
// property being protected is unchanged: an intent with NO snapshot must fall
// back to the ACTIVE rate, never to zero.
func TestReleaseEscrowDoesNotTreatAnAbsentSnapshotAsZeroPercent(t *testing.T) {
	body := releaseEscrowBody(t)

	if strings.Contains(body, "if pct == 0 && fixed == 0 {") {
		t.Errorf("ReleaseEscrow is back to inferring 'no snapshot' from a zero rate.\n"+
			"The columns are NOT NULL DEFAULT 0, so a genuine 0%% rate reads the same "+
			"as an absent one. Use commission_snapshot_present.\n%s", body)
	}

	at := strings.Index(body, "if !intent.CommissionSnapshotPresent {")
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
	// is the same defect wearing a hat -- and mutation checking proved it, twice:
	// the obvious mutants did not even compile (`cfg` became unused), so the
	// negative assertion was never exercised.
	if !strings.Contains(fallback, "pct, fixed = cfg.Pct, cfg.Fixed") {
		t.Errorf("the fallback does not assign the active rate to both pct and fixed.\n%s\n"+
			"Anything else here means either zero (commission never charged on legacy "+
			"intents) or a partial rate", fallback)
	}
}

// An intent WITH a snapshot must use it even when it is (0, 0). That is the whole
// point of 00052: a 0% rate that was read at charge time is a real rate and must
// survive an admin raising the active rate before delivery.
func TestReleaseEscrowHonoursAZeroRateSnapshot(t *testing.T) {
	body := releaseEscrowBody(t)

	// The snapshot must be read into pct/fixed BEFORE the presence test, so the
	// present case keeps it.
	readAt := strings.Index(body, "pct, fixed := intent.CommissionRatePct, intent.CommissionRateFixed")
	presenceAt := strings.Index(body, "if !intent.CommissionSnapshotPresent {")
	if readAt < 0 || presenceAt < 0 {
		t.Fatal("the snapshot read or the presence test is missing; update this test")
	}
	if readAt > presenceAt {
		t.Errorf("the snapshot is read AFTER the presence test (read %d, test %d).\n"+
			"The present case must keep the snapshot, including a legitimate (0, 0)", readAt, presenceAt)
	}
}

// The refund side: an intent with no snapshot falls back to the active rate, and
// an intent with one uses the snapshot. Both must derive the legs through
// repository.Commission rather than assigning a fee directly.
func TestRefundPathUsesTheSnapshotWhenPresentAndActiveRateWhenNot(t *testing.T) {
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

	if !strings.Contains(fallback, "if !intent.CommissionSnapshotPresent {") {
		t.Errorf("the refund path does not test for a snapshot.\n%s\n"+
			"It should use the snapshot when one exists -- that is the rate card in "+
			"force when the charge was taken -- and the active rate only when there "+
			"is none", fallback)
	}
	if !strings.Contains(fallback, "ActiveFeeTx(") {
		t.Errorf("the refund path does not fall back to the active rate for an intent "+
			"with no snapshot.\n%s\n"+
			"Recomputing at zero would refund a fee the platform never charged", fallback)
	}
	if !strings.Contains(fallback, "repository.Commission(pct, fixed, intent.Amount)") {
		t.Errorf("the refund fallback does not derive the legs from the resolved rate.\n%s\n"+
			"Anything else means the reversal is not the fee that was actually taken", fallback)
	}
	// And it must not reach for the active rate unconditionally, which is what it
	// did while its own comment claimed the snapshot was the correct source.
	activeFirst := strings.Index(fallback, "ActiveFeeTx(")
	snapshotTest := strings.Index(fallback, "if !intent.CommissionSnapshotPresent {")
	if activeFirst >= 0 && snapshotTest > activeFirst {
		t.Error("the active rate is read before the snapshot is tested, so the snapshot " +
			"would never be used")
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
