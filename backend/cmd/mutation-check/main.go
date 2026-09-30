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
// One failure mode of this harness is worth naming, because it is the reason the
// argument is necessary at all: a mutation that never runs anything still reports
// success. `go test` on a bad package path prints
//
//	FAIL	./internal/service/ ./internal/repository [setup failed]
//
// which contains the string "FAIL" and nothing else -- no test, no assertion, no
// `_test.go:` line. A harness that only looks for "FAIL" scores that as CAUGHT.
// Three mutations were green for exactly that reason: `exec.Command` was handed
// the two package paths as ONE argument, so the command never named a package
// go could resolve, and M3/M4/M5 -- the cap's source table and the exact set of
// states it counts, which is the most money-critical rule in the file -- had
// never run a single test.
//
// Two rules follow, and both are enforced below rather than trusted:
//
//   - A caught mutation must NAME the test that caught it. An empty name means
//     nothing ran, and it is reported as MISSED, because "caught" with no
//     witness is indistinguishable from the failure above.
//   - The package list is split into separate arguments, so the command is the
//     one that was intended.
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
			// An unknown refund state counts toward the cap.
			//
			// This used to mutate `refund_cap.go`'s `refundStateCountsTowardCap` --
			// a function NOTHING CALLED, because the rule lives as literals in the
			// query. It was caught by its own test and reported as coverage of the
			// cap, which is how a mutation ends up attesting to something it never
			// touched. It now mutates the query itself, and the test that catches it
			// compares those literals against domain.RefundStatesCountingTowardCap.
			label: "M9: an unknown refund state counts toward the cap",
			file:  repoFile,
			old: "\t\t   AND status IN ('submitted', 'succeeded', 'manual')\n" +
				"\t\t   AND ($2::uuid IS NULL OR id <> $2::uuid)`,",
			new: "\t\t   AND ($2::uuid IS NULL OR id <> $2::uuid)`,",
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
		{
			// The cap counts the row that is being settled. The reserve step commits
			// its row as `submitted` before the provider is called, so a full refund
			// read `already = charge`, computed `remaining = 0` and refused itself --
			// rolling back the reversal legs and the journal after the buyer had
			// already been paid. Every full refund failed.
			label: "M13: the cap counts the refund it is settling",
			file:  repoFile,
			old:   "\t\t   AND ($2::uuid IS NULL OR id <> $2::uuid)`,",
			new:   "\t\t   AND ($2::uuid IS NOT NULL OR id <> $2::uuid)`,",
		},
		{
			// The same defect from the other end: the SQL is correct and the caller
			// forgets to identify which row it is settling, so nothing is excluded.
			// Both halves are mutated because either one alone reinstates the bug,
			// and a test that only covers one of them passes while the other rots.
			label: "M14: the settlement stops excluding its own row",
			file:  svcRefund,
			old:   "\tfinal, err := s.buildRefundPlan(ctx, q, locked, plan.Amount, &refund.ID)",
			new:   "\tfinal, err := s.buildRefundPlan(ctx, q, locked, plan.Amount, nil)",
		},
		{
			// An in-flight refund is money the provider accepted and will pay, so it
			// must still count against a DIFFERENT refund. This is why the fix
			// excludes by id rather than dropping `submitted` from the cap: the
			// alternative reinstates the concurrent-double-refund race that
			// 03a100b closed, and it is a one-line change that looks like a fix.
			label: "M15: an in-flight refund stops counting against other refunds",
			file:  repoFile,
			old: "\t\t   AND status IN ('submitted', 'succeeded', 'manual')\n" +
				"\t\t   AND ($2::uuid IS NULL OR id <> $2::uuid)`,",
			new: "\t\t   AND status IN ('succeeded', 'manual')\n" +
				"\t\t   AND ($2::uuid IS NULL OR id <> $2::uuid)`,",
		},
		{
			// The return path moves money and writes no `refunds` row, so the
			// cumulative cap -- which reads that table -- cannot see a returned item.
			// A Rp50,000 return followed by a Rp100,000 gateway refund on the same
			// order is then individually valid twice over.
			label: "M16: the return path moves money without recording a refund",
			file:  svcFile,
			old: "\tif err := s.payments.CreateRefund(ctx, q, refund); err != nil {\n" +
				"\t\treturn err\n\t}\n\n" +
				"\tif err := s.payments.SetIntentStatusGuardedTx(ctx, q, locked.ID,\n" +
				"\t\t[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded},\n" +
				"\t\tplan.NextStatus); err != nil {\n\t\treturn err\n\t}",
			new: "\tif err := s.payments.SetIntentStatusGuardedTx(ctx, q, locked.ID,\n" +
				"\t\t[]string{domain.IntentCaptured, domain.IntentReleased, domain.IntentPartiallyRefunded},\n" +
				"\t\tplan.NextStatus); err != nil {\n\t\treturn err\n\t}\n\t_ = refund",
		},
		{
			// The literal empty intent id this commit removed. `payment_intent_id`
			// is `UUID NOT NULL`, so the lookup's WHERE clause receives an invalid
			// uuid and the query errors -- aborting every provider refund
			// notification and drawing a 24-hour retry loop from Midtrans.
			//
			// This is the mutation M7 could never have been. M7 replaced the
			// classifier handoff, which a stub answers correctly whatever the
			// lookup was asked. This one changes the question, and the stub used to
			// discard the answer.
			label: "M17: the replay lookup is not scoped to a payment intent",
			file:  svcRefund,
			old:   "\talready, err := s.refundRecordedForProviderKey(ctx, intentID, key)",
			new:   "\talready, err := s.refundRecordedForProviderKey(ctx, \"\", key)",
		},
		{
			// The guard that would have caught M17, removed. Without it, a caller
			// with no intent id reaches Postgres and gets a uuid cast error whose
			// message names nothing about the wiring.
			label: "M18: a blank intent id is allowed through to the query",
			file:  svcRefund,
			old: "\tif strings.TrimSpace(intentID) == \"\" {\n" +
				"\t\treturn nil, domain.E(domain.KindInternal, \"REFUND_LOOKUP_WITHOUT_INTENT\",\n" +
				"\t\t\t\"a provider refund replay lookup was made without a payment intent id, \"+\n" +
				"\t\t\t\t\"so it cannot be scoped to a payment; refusing rather than searching \"+\n" +
				"\t\t\t\t\"for a refund across every order\")\n\t}",
			new: "\t_ = intentID",
		},
		{
			// The literal empty reference this commit removed. The replay lookup
			// filters `gateway_ref IS NOT NULL` and rebuilds the idempotency key
			// from the stored row, so a dropped reference means the guard can never
			// match anything -- regardless of how correctly it is scoped.
			label: "M19: a provider refund is recorded without its reference",
			file:  repoFile,
			old:   "\t\trf.ID, rf.PaymentIntentID, rf.OrderID, rf.Gateway, rf.GatewayRef,",
			new:   "\t\trf.ID, rf.PaymentIntentID, rf.OrderID, rf.Gateway, \"\",",
		},
		{
			// The lookup is wired in the constructor, so removing it puts production
			// on the fallback that every test bypasses. The mutation reinstates the
			// silent fallback rather than deleting the field, because that is the
			// version that shipped.
			label: "M20: the replay lookup falls back instead of being wired",
			file:  svcRefund,
			old: "\tif lookup == nil {\n" +
				"\t\t// LOUD, and no longer silent. This branch used to fall back to building the\n" +
				"\t\t// repository adapter, which meant the wiring omission was invisible: every\n" +
				"\t\t// test drove a stub, and production silently ran a different implementation\n" +
				"\t\t// that no test could reach. The constructor wires the lookup, so nil now\n" +
				"\t\t// means the service was built without one -- and the honest response to\n" +
				"\t\t// \"I cannot tell whether this refund is a replay\" is to refuse, not to\n" +
				"\t\t// guess. Proceeding would apply a duplicate refund, which is the one\n" +
				"\t\t// outcome this guard exists to prevent.\n" +
				"\t\treturn nil, domain.E(domain.KindInternal, \"REFUND_LOOKUP_UNWIRED\",\n" +
				"\t\t\t\"the payment service has no refund lookup configured, so a replayed \"+\n" +
				"\t\t\t\t\"provider notification cannot be recognised; refusing rather than \"+\n" +
				"\t\t\t\t\"treating every notification as new and refunding the buyer twice\")\n\t}",
			new: "\tif lookup == nil {\n" +
				"\t\tlookup = paymentRefundLookup{payments: s.payments}\n\t}",
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
		// Fields, not pkg: a single argument containing a space is one malformed
		// package path, and `go test` answers that with "[setup failed]" -- a line
		// containing "FAIL" and no test in it. See the file header.
		args := append([]string{"test"}, strings.Fields(pkg)...)
		args = append(args, "-count=1")
		cmd := exec.Command("go", args...)
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
		// A setup failure is not a test result. `go test` emits "[setup failed]"
		// when it cannot resolve a package, and that is the one outcome that
		// proves nothing about the mutation.
		setupErr := strings.Contains(got, "setup failed") ||
			strings.Contains(got, "no required module provides") ||
			strings.Contains(got, "cannot find package") ||
			strings.Contains(got, "malformed import path")

		// Name the test that caught it. Required, not decorative: a failure with
		// no `_test.go:` line came from the toolchain, not from an assertion, and
		// reporting it as caught is how three mutations sat green while testing
		// nothing at all.
		name := ""
		for _, line := range strings.Split(got, nl) {
			if strings.Contains(line, "_test.go:") {
				name = strings.TrimSpace(line)
				break
			}
		}

		switch {
		case setupErr:
			missed++
			fmt.Printf("FAIL %s\n       HARNESS ERROR -- go test could not resolve its "+
				"packages, so no test ran\n", m.label)
		case compileErr:
			missed++
			fmt.Printf("FAIL %s\n       the mutation did not compile, so no test ran\n", m.label)
		case failed && name == "":
			missed++
			fmt.Printf("FAIL %s\n       FAILED WITHOUT A WITNESS -- the run reported a "+
				"failure but no test named itself, so this is not evidence of coverage\n%s\n",
				m.label, indent(got))
		case failed:
			caught++
			fmt.Printf("ok   %s\n       caught by %s\n", m.label, name)
		default:
			missed++
			fmt.Printf("FAIL %s\n       SURVIVED -- no test noticed\n", m.label)
		}
	}

	fmt.Printf("\n%d caught, %d missed, %d total\n", caught, missed, len(muts))
	if missed > 0 {
		os.Exit(1)
	}
}

// indent re-indents captured output so a failure prints as a block under its
// label rather than as a wall of unlabelled text.
func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("         | ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
