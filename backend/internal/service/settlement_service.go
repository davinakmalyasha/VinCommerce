package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/vincommerce/backend/internal/repository"
)

// Settlement reconciliation: comparing what the gateway says moved against what
// our ledger says happened.
//
// Why this exists, stated as the concrete alternative it replaces. Before it, the
// platform could compare its books to itself -- and it did that very accurately,
// which is the trap. A self-consistent ledger that has never been compared to a
// bank statement agrees with itself and with nothing else. Tracing the original
// defect: the books said Rp2,000 while the platform held Rp100,000, and no table
// anywhere recorded the Rp100,000 arriving. Nothing could contradict anything,
// because there was nothing to contradict it with.

// SettlementReport is the outcome of one reconciliation pass.
type SettlementReport struct {
	GeneratedAt time.Time
	// Unreconciled is how many gateway movements have no journal at all.
	Unreconciled int
	// Matched is how many are linked to a journal.
	Matched int
	// Discrepant is how many are linked but disagree on the amount. This is the
	// number that matters: a matched-but-wrong settlement means the entry exists
	// and the amount is still wrong.
	Discrepant int
	// UnmatchedSamples is what an operator looks at first. The report is a count;
	// a count alone is a finding nobody triages.
	UnmatchedSamples []string
	// DiscrepantSamples is the same for amount disagreements.
	DiscrepantSamples []string
	// Clean is true only when every gateway movement is matched AND agrees.
	Clean bool
}

// ReconcileSettlements compares gateway-reported movements against the ledger.
//
// The comparison is one-sided by design: it starts from the gateway's view and
// asks whether we have a matching entry. The reverse -- starting from our
// journals and asking whether a settlement exists -- would find nothing to report
// for a movement WE recorded that the gateway never saw, which is the case where
// we have invented a transaction.
//
// A non-zero `amount_delta` is reported, never corrected automatically. The
// gateway's record is the one we most need to keep; overwriting it with our own
// figure would destroy the only evidence that the two disagree.
func (s *PaymentService) ReconcileSettlements(ctx context.Context, limit int) (*SettlementReport, error) {
	matches, err := s.payments.SettlementMatches(ctx, true, limit)
	if err != nil {
		return nil, err
	}
	return buildSettlementReport(matches), nil
}

// buildSettlementReport turns the raw matches into the report.
//
// Split out from ReconcileSettlements, which does the read, so the DECISION --
// what counts as clean, what is discrepant, which is a finding worth naming --
// is a pure function and can be tested without a database.
//
// That split is not cosmetic. Two mutations survived while this was inline behind
// a repository call: marking the report clean when gateway movements had no
// journal, and computing a discrepancy count without acting on it. Both are the
// failure this report exists to prevent, and both were invisible because
// deciding them required a live database.
func buildSettlementReport(matches []*repository.SettlementMatch) *SettlementReport {
	report := &SettlementReport{GeneratedAt: time.Now().UTC(), Clean: true}
	for _, m := range matches {
		if !m.IsMatched {
			report.Unreconciled++
			if len(report.UnmatchedSamples) < maxReportSamples {
				report.UnmatchedSamples = append(report.UnmatchedSamples, describeUnmatched(m))
			}
			continue
		}
		report.Matched++
		if !amountsAgree(m.AmountDelta) {
			report.Discrepant++
			if len(report.DiscrepantSamples) < maxReportSamples {
				report.DiscrepantSamples = append(report.DiscrepantSamples, describeDiscrepant(m))
			}
		}
	}
	// CLEAN means every gateway movement is matched AND every one agrees. Both
	// conditions, either of which on its own is a finding:
	//
	//   an unreconciled movement is money the gateway reported and no journal
	//     records. The entry is missing, so there is no amount to compare.
	//   a discrepant one is matched but the amounts differ. The entry exists and
	//     the money is still wrong -- which is worse, because it looks handled.
	//
	// Setting Clean from only the second, or only the first, is how a
	// reconciliation that reports "all good" while a settlement has no entry at
	// all gets written.
	report.Clean = report.Unreconciled == 0 && report.Discrepant == 0
	return report
}

// maxReportSamples bounds how many offenders a report names.
//
// A finding nobody can act on is a log nobody reads, and naming every one of ten
// thousand unreconciled rows is the same as naming none. The COUNTS are
// complete; the samples are a starting point.
const maxReportSamples = 10

// amountsAgree compares a gateway-reported net against our journal total.
//
// The tolerance is one rupiah, not a fraction. These are whole-rupiah figures,
// so a fraction of a percent on a Rp100,000,000 settlement is Rp100,000 of slack
// -- a tolerance that grows with the size of the number is a tolerance that
// eventually waves through a real discrepancy.
//
// One rupiah covers the float64 round-trip through `NUMERIC`, which is exact for
// values this small but is not a licence to be vague.
func amountsAgree(delta float64) bool { return math.Abs(delta) < 1.0 }

// settlementRefFor builds the idempotency key for a settlement lookup.
//
// Keyed on the gateway, the kind, the reference AND the date, because a provider
// may reuse a reference string across settlement days, and treating the second
// day's line as a duplicate would silently drop a real movement of cash.
func settlementRefFor(gateway, kind, ref string, settledOn time.Time) string {
	parts := []string{gateway, kind, strings.TrimSpace(ref)}
	if !settledOn.IsZero() {
		parts = append(parts, settledOn.UTC().Format("2006-01-02"))
	}
	return strings.Join(parts, "|")
}

// describeUnmatched renders a gateway movement with no journal, for the report.
func describeUnmatched(m *repository.SettlementMatch) string {
	ref := m.SettlementRef
	if ref == "" {
		ref = "(no reference)"
	}
	return fmt.Sprintf("%s %s Rp%.0f settled %s ref=%s -- no journal records this movement",
		m.Gateway, m.Kind, m.Net, m.SettledOn.Format("2006-01-02"), ref)
}

// describeDiscrepant renders a matched movement whose amount disagrees.
func describeDiscrepant(m *repository.SettlementMatch) string {
	return fmt.Sprintf("%s %s ref=%s: gateway says Rp%.0f, ledger recorded Rp%.0f (delta Rp%.0f)",
		m.Gateway, m.Kind, m.SettlementRef, m.Net, m.JournalTotal, m.AmountDelta)
}
