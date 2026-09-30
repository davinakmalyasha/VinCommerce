// Command mutation-check proves the refund guards can actually fail.
//
// It reintroduces each defect the double-refund fix removes, runs the affected
// package, and reports which test caught it. A guard that cannot fail is worse
// than no guard, because it is read as evidence of something -- and three of the
// defects below survived their first version of the tests:
//
//	M2  a gateway call written as `g.Refund(...)` rather than `gw.Refund(...)`
//	    matched none of the literal call patterns the reachability check used
//	M2  `if g, err := s.gatewayFor(m); err == nil {` was skipped entirely,
//	    because the parser took the `=` from `err == nil` and rejected the line
//	M7  a unit test of the classifier passed `true` as a literal and never
//	    exercised the lookup that produces it
//
// Each was found by a surviving mutation, not by reading the code. That is the
// argument for running this rather than trusting the tests.
//
// Run from the backend directory:
//
//	go run ./cmd/mutation-check
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type mutation struct {
	label  string
	file   string
	old    string
	new    string
	expect string // substring expected in the failing output, "" for any failure
}

const (
	svcFile     = "internal/service/payment_service.go"
	svcRefund   = "internal/service/refund_service.go"
	repoFile    = "internal/repository/payment_repo.go"
	capFile     = "internal/service/refund_cap.go"
	servicePkg  = "./internal/service/"
	serviceAndR = "./internal/service/ ./internal/repository/"
)

func main() {
	nl := "\n"

	muts := []mutation{
		{
			label: "M1: onRefunded resubmits via RefundOrder (THE DOUBLE-REFUND)",
			file:  svcFile,
			old:   "\t_, err := s.RecordProviderRefund(ctx, providerRefundEvent{",
			new: "\t_ = s.RefundOrder(ctx, intent.OrderID, \"gateway refund\", true, amount)\n" +
				"\t_, err := s.RecordProviderRefund(ctx, providerRefundEvent{",
			expect: "",
		},
		{
			// A gateway call planted directly inside RecordProviderRefund. The
			// earlier version of this mutation patched a `key := providerRefundKey(ev)`
			// line that has since moved into replayIsRecognised, so it silently
			// stopped testing anything -- a mutation harness that drifts from the
			// code it is supposed to attack is worse than none, because it reports
			// coverage that is not there.
			label: "M2: RecordProviderRefund itself calls the gateway",
			file:  svcRefund,
			old:   "	switch classifyProviderRefund(ev, false) {",
			new: "	if g, gerr := s.gatewayFor(ev.Gateway); gerr == nil {\n" +
				"		_, _ = g.Refund(ctx, payments.RefundInput{Reference: ev.Reference, Amount: ev.Amount})\n" +
				"	}\n" +
				"	switch classifyProviderRefund(ev, false) {",
		},
		{
			label: "M3: the cap goes back to reading wallet_transactions",
			file:  repoFile,
			old: "		SELECT COALESCE(SUM(amount), 0)::float8\n" +
				"		  FROM refunds\n" +
				"		 WHERE order_id = $1\n" +
				"		   AND status IN ('submitted', 'succeeded', 'manual')",
			new: "		SELECT COALESCE(SUM(amount), 0)::float8\n" +
				"		  FROM wallet_transactions\n" +
				"		 WHERE ref_id = $1 AND reason = 'refund' AND kind = 'credit'",
		},
		{
			label: "M4: the cap counts the 'failed' state",
			file:  repoFile,
			old:   "		   AND status IN ('submitted', 'succeeded', 'manual')",
			new:   "		   AND status IN ('submitted', 'succeeded', 'manual', 'failed')",
		},
		{
			label: "M5: the cap stops counting 'manual' (money we owe, not yet sent)",
			file:  repoFile,
			old:   "		   AND status IN ('submitted', 'succeeded', 'manual')",
			new:   "		   AND status IN ('submitted', 'succeeded')",
		},
		{
			// The commission leg is dropped. A partial refund then reverses only the
			// seller's share, so the platform keeps commission on a sale it
			// refunded -- the exact defect 03a100b fixed, reintroduced here.
			label: "M6: the commission reversal is dropped",
			file:  capFile,
			old:   "\trefundFee = moneyRound(fee * (amount / charge))",
			new:   "\trefundFee = 0",
		},
		{
			// The idempotency lookup is ignored, so a replayed webhook is applied a
			// second time. Providers retry for up to 24 hours, which makes a
			// duplicate notification the single most common event in the system.
			label: "M7: a replayed provider notification is applied again",
			file:  svcRefund,
			old:   "	if classifyProviderRefund(ev, already != nil) == refundDecisionAlreadyApplied {",
			new:   "	if false {",
		},
		{
			label: "M8: an unidentifiable refund is applied rather than escalated",
			file:  capFile,
			old:   "	if strings.TrimSpace(ev.Reference) == \"\" {",
			new:   "	if false {",
		},
		{
			label: "M9: an unknown refund state counts toward the cap",
			file:  capFile,
			old:   "	case RefundStateSubmitted, RefundStateSucceeded, RefundStateManual:\n\t\treturn true\n\tdefault:\n\t\treturn false",
			new:   "	case RefundStateSubmitted, RefundStateSucceeded, RefundStateManual:\n\t\treturn true\n\tdefault:\n\t\treturn true",
		},
		{
			label: "M10: the provider key drops the gateway, so gateways collide",
			file:  capFile,
			old:   "\treturn fmt.Sprintf(\"provider:%s:%s:%.0f\", ev.Gateway, ref, ev.Amount)",
			new:   "\treturn fmt.Sprintf(\"provider:%s:%.0f\", ref, ev.Amount)",
		},
		{
			label: "M11: the provider key stops rounding to whole rupiah",
			file:  capFile,
			old:   "\treturn fmt.Sprintf(\"provider:%s:%s:%.0f\", ev.Gateway, ref, ev.Amount)",
			new:   "\treturn fmt.Sprintf(\"provider:%s:%s:%f\", ev.Gateway, ref, ev.Amount)",
		},
		{
			label: "M12: a whitespace reference is treated as a real one",
			file:  capFile,
			old:   "\tref := strings.TrimSpace(ev.Reference)\n\tif ref == \"\" {",
			new:   "\tref := ev.Reference\n\tif len(ref) == 0 {",
		},
	}

	caught, missed := 0, 0
	for _, m := range muts {
		orig, err := os.ReadFile(m.file)
		if err != nil {
			fmt.Printf("SKIP %s: %v\n", m.label, err)
			continue
		}
		body := string(orig)
		if !strings.Contains(body, m.old) {
			fmt.Printf("SKIP %s: patch did not apply\n", m.label)
			continue
		}
		mutated := strings.Replace(body, m.old, m.new, 1)
		if err := os.WriteFile(m.file, []byte(mutated), 0o644); err != nil {
			fmt.Printf("SKIP %s: %v\n", m.label, err)
			continue
		}
		// Restore on EVERY exit path, including a panic. Without this, a
		// Ctrl-C mid-run leaves a mutated source file in the working tree and the
		// next test run fails for a reason that has nothing to do with the code --
		// which happened during development and cost a confused debugging detour.
		defer func(path string, content []byte) {
			_ = os.WriteFile(path, content, 0o644)
		}(m.file, orig)

		pkg := servicePkg
		if m.file == repoFile {
			pkg = serviceAndR
		}
		cmd := exec.Command("go", "test", pkg, "-count=1")
		out, _ := cmd.CombinedOutput()
		os.WriteFile(m.file, orig, 0o644)

		got := strings.TrimSpace(string(out))
		failed := strings.Contains(got, "FAIL") || strings.Contains(got, "build failed")
		// A build failure counts as "caught" only if it is not a compile error in
		// the mutation itself.
		compileErr := strings.Contains(got, "build failed") && strings.Contains(got, ".go:")
		if strings.Contains(got, "cannot use") || strings.Contains(got, "undefined:") {
			compileErr = true
		}

		if failed && !compileErr {
			caught++
			// Name the test that caught it.
			name := ""
			for _, line := range strings.Split(got, nl) {
				if strings.Contains(line, "_test.go:") {
					name = strings.TrimSpace(line)
					break
				}
			}
			fmt.Printf("ok   %s\n       caught by %s\n", m.label, name)
		} else if compileErr {
			missed++
			fmt.Printf("FAIL %s\n       the mutation did not compile, so no test ran\n", m.label)
		} else {
			missed++
			fmt.Printf("FAIL %s\n       SURVIVED -- no test noticed\n", m.label)
		}
	}

	fmt.Printf("\n%d caught, %d missed, %d total\n", caught, missed, len(muts))
	if missed > 0 {
		os.Exit(1)
	}
}
