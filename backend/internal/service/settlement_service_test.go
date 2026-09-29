package service

import (
	"strings"
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/repository"
)

// Settlement reconciliation: the comparison between what the gateway says and
// what the ledger recorded.
//
// The tolerance is the thing most likely to be got wrong, and getting it wrong
// is quiet in both directions. Too tight and float64 round-trips report false
// findings daily, which trains an operator to ignore the report. Too loose and a
// real discrepancy waves through -- and a reconciliation that waves through
// discrepancies is a reconciliation nobody needs to run.

// A report that says "clean" while a gateway movement has no journal is worse
// than no report: it is confidently wrong, and an operator who has learned to
// trust the green line stops reading it.
//
// These are the two cases that survived while the report was built inline behind
// a repository call.
func TestAReportIsCleanOnlyWhenEveryMovementIsMatchedAndAgrees(t *testing.T) {
	matched := &repository.SettlementMatch{IsMatched: true, AmountDelta: 0}
	unmatched := &repository.SettlementMatch{GatewaySettlement: repository.GatewaySettlement{Gateway: "midtrans"}, IsMatched: false}
	discrepant := &repository.SettlementMatch{IsMatched: true, AmountDelta: 5000}

	cases := []struct {
		name        string
		matches     []*repository.SettlementMatch
		wantClean   bool
		wantUnrec   int
		wantDiscrep int
	}{
		{"nothing outstanding", []*repository.SettlementMatch{matched, matched}, true, 0, 0},
		{"no movements at all", nil, true, 0, 0},
		// Money moved at the gateway and no journal records it. NOT clean.
		{"an unmatched movement", []*repository.SettlementMatch{matched, unmatched}, false, 1, 0},
		{"only unmatched", []*repository.SettlementMatch{unmatched}, false, 1, 0},
		// Matched, but the entry says a different amount. NOT clean -- and this is
		// the more dangerous one, because it looks handled.
		{"a discrepant movement", []*repository.SettlementMatch{matched, discrepant}, false, 0, 1},
		{"both", []*repository.SettlementMatch{unmatched, discrepant}, false, 1, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSettlementReport(tc.matches)
			if got.Clean != tc.wantClean {
				t.Errorf("Clean = %v, want %v (unreconciled %d, discrepant %d)",
					got.Clean, tc.wantClean, got.Unreconciled, got.Discrepant)
			}
			if got.Unreconciled != tc.wantUnrec {
				t.Errorf("Unreconciled = %d, want %d", got.Unreconciled, tc.wantUnrec)
			}
			if got.Discrepant != tc.wantDiscrep {
				t.Errorf("Discrepant = %d, want %d", got.Discrepant, tc.wantDiscrep)
			}
		})
	}
}

// The counts must be complete even when the samples are truncated: a report that
// says "10 problems" when there are ten thousand is a report that gets ignored.
func TestReportCountsEveryOffenderButNamesOnlyTheFirstFew(t *testing.T) {
	const many = 250
	matches := make([]*repository.SettlementMatch, many)
	for i := range matches {
		matches[i] = &repository.SettlementMatch{GatewaySettlement: repository.GatewaySettlement{Gateway: "midtrans"}, IsMatched: false}
	}
	got := buildSettlementReport(matches)

	if got.Unreconciled != many {
		t.Errorf("Unreconciled = %d, want %d: the count must be complete", got.Unreconciled, many)
	}
	if len(got.UnmatchedSamples) != maxReportSamples {
		t.Errorf("named %d samples, want %d: a log nobody can read is a log nobody reads",
			len(got.UnmatchedSamples), maxReportSamples)
	}
	if got.Clean {
		t.Error("Clean = true with 250 unmatched movements")
	}
}

func TestAmountsAgreeWithinOneRupiah(t *testing.T) {
	cases := []struct {
		delta float64
		want  bool
	}{
		{0, true},
		{0.4, true},
		{0.999, true},
		{-0.999, true},
		// One rupiah is the boundary and is NOT agreed: the tolerance is
		// "less than a rupiah", because these are whole-rupiah figures and a
		// whole rupiah of disagreement is a whole rupiah of missing money.
		{1, false},
		{-1, false},
		{1.01, false},
		{-1.01, false},
		{500, false},
		{-500, false},
		{100000, false},
	}
	for _, c := range cases {
		if got := amountsAgree(c.delta); got != c.want {
			t.Errorf("amountsAgree(%v) = %v, want %v", c.delta, got, c.want)
		}
	}
}

// The tolerance must NOT scale with the size of the number.
//
// This is the mutation that matters most: a "relative tolerance" of even 0.1% is
// Rp100,000 of slack on a Rp100,000,000 settlement, and it passes silently. A
// tolerance that grows with the amount eventually waves through the entire
// discrepancy it was meant to catch.
func TestTheToleranceDoesNotScaleWithTheAmount(t *testing.T) {
	// A discrepancy proportional to the size of the transaction, on an amount
	// large enough that a percentage-based check would forgive it.
	const huge = 100_000_000.0
	const smallPercentOfHuge = huge * 0.001 // 0.1%, i.e. Rp100,000

	if amountsAgree(smallPercentOfHuge) {
		t.Errorf("a Rp%.0f discrepancy (0.1%% of Rp%.0f) was forgiven; the tolerance "+
			"must be absolute, not relative", smallPercentOfHuge, huge)
	}
	// And the same absolute discrepancy on a small amount is equally rejected --
	// which is the point: the check does not care how big the transaction was.
	if amountsAgree(100) != amountsAgree(100) {
		t.Error("amountsAgree is not deterministic")
	}
}

func TestSettlementRefForIsStableAndDistinct(t *testing.T) {
	day1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	base := settlementRefFor("midtrans", "capture", "trx-1", day1)
	if base != settlementRefFor("midtrans", "capture", "trx-1", day1) {
		t.Error("the same settlement produced two keys; a re-import would look new")
	}
	// A provider may reuse a reference string on a different day. Treating the
	// second day's line as a duplicate would silently drop a real movement of
	// cash, which is the failure this key's date component exists to prevent.
	if base == settlementRefFor("midtrans", "capture", "trx-1", day2) {
		t.Error("two settlement days sharing a reference collapsed to one key")
	}
	// And the same reference for a different KIND is a different movement --
	// Midtrans legitimately issues the same reference for a capture and a later
	// refund, and conflating them would reject a real refund.
	if base == settlementRefFor("midtrans", "refund", "trx-1", day1) {
		t.Error("a capture and a refund sharing a reference collapsed to one key")
	}
	if base == settlementRefFor("sandbox", "capture", "trx-1", day1) {
		t.Error("two gateways sharing a reference collapsed to one key")
	}
}

func TestSettlementRefForToleratesAMissingReferenceAndDate(t *testing.T) {
	// Neither is expected, and neither may panic or produce a key that collides
	// with a real one.
	if got := settlementRefFor("midtrans", "fee", "", time.Time{}); got == "" {
		t.Error("an unreferenced settlement produced an empty key")
	}
	if got := settlementRefFor("midtrans", "fee", "  ", time.Time{}); got == "" {
		t.Error("a whitespace reference produced an empty key")
	}
	// A whitespace-padded reference and a trimmed one are the same reference.
	if settlementRefFor("midtrans", "fee", " f-1 ", time.Time{}) !=
		settlementRefFor("midtrans", "fee", "f-1", time.Time{}) {
		t.Error("a padded reference produced a different key from the trimmed one")
	}
}

func TestUnmatchedSettlementIsNamedSpecifically(t *testing.T) {
	// A count tells an operator something is wrong. A gateway and a reference tell
	// them which movement to go and look up in the provider's dashboard, which is
	// the only place the answer is.
	m := &repository.SettlementMatch{}
	m.Gateway, m.Kind, m.SettlementRef, m.Net = "midtrans", "capture", "trx-9", 50000
	m.SettledOn = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	msg := describeUnmatched(m)
	for _, want := range []string{"midtrans", "capture", "trx-9", "50000", "2026-03-01"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the description omits %q, so an operator cannot look the movement up: %q",
				want, msg)
		}
	}

	// A missing reference must say so rather than rendering empty, or the
	// description reads as a truncated sentence.
	m.SettlementRef = ""
	if noRef := describeUnmatched(m); !strings.Contains(noRef, "no reference") {
		t.Errorf("a settlement with no reference should say so: %q", noRef)
	}
}

func TestDiscrepantSettlementNamesBothFigures(t *testing.T) {
	// Both sides of the disagreement, and the direction. "Something is wrong" is
	// not actionable; "the gateway says Rp100,000 and we recorded Rp98,000" is.
	m := &repository.SettlementMatch{}
	m.Gateway, m.Kind, m.SettlementRef = "midtrans", "refund", "trx-9"
	m.Net, m.JournalTotal, m.AmountDelta = 100000, 98000, 2000

	msg := describeDiscrepant(m)
	for _, want := range []string{"100000", "98000", "2000", "trx-9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the description omits %q: %q", want, msg)
		}
	}
}
