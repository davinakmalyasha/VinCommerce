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
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

type mutation struct {
	label  string
	file   string
	old    string
	new    string
	expect string // substring expected in the failing output, "" for any failure
}

const (
	svcFile    = "internal/service/payment_service.go"
	svcRefund  = "internal/service/refund_service.go"
	sellerFile = "internal/service/seller_service.go"
	// svcPostFile holds the journal-posting helpers. Mutated separately because the
	// distinction between the swallowing `postLedger` and the strict
	// `postLedgerStrict` IS the guard, and a mutation that swaps a call site cannot
	// express it without failing to compile.
	svcPostFile    = "internal/service/ledger_postings.go"
	repoFile       = "internal/repository/payment_repo.go"
	capFile        = "internal/service/refund_cap.go"
	domainFile     = "internal/domain/order.go"
	shipFile       = "internal/repository/shipment_repo.go"
	orderRepoFile  = "internal/repository/order_repo.go"
	shipSvcFile    = "internal/service/shipment_service.go"
	carrierFile    = "internal/carrier/carrier.go"
	manualFile     = "internal/carrier/manual.go"
	returnRepoFile = "internal/repository/return_parcel_repo.go"
	routerFile     = "internal/httpapi/router.go"
	orderSvcFile   = "internal/service/order_service.go"
)

// allPkgs is what a mutation is tested against.
//
// It was `servicePkg` by default with `serviceAndR` for payment_repo.go, i.e. a
// hardcoded list of files with a special case. That list had already gone stale
// once: adding shipment_repo.go produced a mutation that rewrote the repository
// and then ran only tests in ./internal/service, which never read it. The checker
// reported SURVIVED -- a lie. The honest result was "never checked".
//
// The tempting fix is to derive the package from the file's directory. That is
// wrong in the other direction, and the attempt is kept in this comment because
// getting it wrong is instructive: it dropped the count from 39 caught to 30. The
// reason is that this project's tests are largely SOURCE-TEXT ASSERTIONS that live
// in a different package from the file they check -- payout_lag_override_test.go
// sits in internal/service and reads ../repository/payment_repo.go. So the set of
// tests that can observe a given file is not derivable from that file's path, and
// any static list of them rots.
//
// Therefore: run everything. Slower, and correct. A mutation harness that cannot
// distinguish "no test noticed" from "no test ran" manufactures passes that were
// never performed, which is worse than being slow -- it is the 690cec8 defect and
// the cf370f3 defect wearing a new hat.
const allPkgs = "./..."

// backupDir holds a copy of every source file while it is mutated.
//
// The `defer` restore in the mutation loop is not enough, and this file has been
// bitten by that three times. `defer` fires when main RETURNS -- so a run killed
// mid-`go test` leaves the mutation in place, and the next `go test` fails for a
// reason that has nothing to do with the code under test. It happened three times in
// this session, and once the leftover `if false {` was only noticed because a test
// happened to assert on the surrounding condition.
//
// So the original is written to disk BEFORE the mutation and removed only after the
// restore. That survives a SIGKILL, which a signal handler would not.
const backupDir = ".mutation-backup"

// stage backs a file up before mutating it.
//
// The backup keeps the path structure, so the parent directory has to be created
// too -- the first version only made `backupDir` itself and therefore failed for
// every file under internal/. That failure was SAFE (the mutation is refused rather
// than applied without a backup, so the tree is never left mutated) but it skipped
// every mutation in the suite, which is a failure that looks exactly like "all tests
// pass" if the tally is not read carefully.
func stage(path string, content []byte) error {
	dest := filepath.Join(backupDir, filepath.ToSlash(path))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, content, 0o644)
}

// unstage clears a file's backup once it has been restored.
func unstage(path string) {
	_ = os.Remove(filepath.Join(backupDir, filepath.ToSlash(path)))
}

// restoreFile puts a backed-up file back and reports whether it had to.
func restoreFile(path string) bool {
	backup := filepath.Join(backupDir, filepath.ToSlash(path))
	content, err := os.ReadFile(backup)
	if err != nil {
		return false
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false
	}
	_ = os.Remove(backup)
	return true
}

// recoverInterrupted restores anything left mutated by a killed run.
//
// Called at startup, BEFORE any test runs, because the alternative is that the first
// mutation of a fresh run is judged against a tree the previous run corrupted. A
// stale backup is reported loudly rather than applied quietly: it means a previous
// run died, and that is worth knowing.
func recoverInterrupted(files []string) {
	if _, err := os.Stat(backupDir); err != nil {
		return
	}

	// WALK, not ReadDir. The backup preserves the path structure, so the top level
	// of backupDir contains `internal`, `cmd` -- directories. The first version used
	// os.ReadDir and then tried to restore "internal" AS A FILE, printing
	//
	//     FAILED to restore internal -- remove .mutation-backup\internal manually
	//
	// which is both useless advice and a lie: it is a directory, and the real file
	// underneath was still sitting mutated.
	var stale []string
	_ = filepath.WalkDir(backupDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(backupDir, p)
		if rerr != nil {
			return nil
		}
		stale = append(stale, filepath.FromSlash(rel))
		return nil
	})
	if len(stale) == 0 {
		return
	}

	fmt.Printf("recovering %d file(s) left mutated by an interrupted run:\n", len(stale))
	for _, rel := range stale {
		if restoreFile(rel) {
			fmt.Printf("  restored %s\n", rel)
		} else {
			fmt.Printf("  FAILED to restore %s -- copy it back from %s manually\n",
				rel, filepath.Join(backupDir, rel))
		}
	}
	fmt.Println()
}

// installSignalGuard restores the in-flight mutation on Ctrl-C.
//
// This covers the polite case; stage/recover covers the impolite one. Both exist
// because a long run WILL be interrupted -- these runs take minutes and a terminal
// timeout is a normal thing to hit.
func installSignalGuard() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-ch
		if inFlight.path != "" {
			fmt.Printf("\nreceived %s; restoring %s\n", sig, inFlight.path)
			if restoreFile(inFlight.path) {
				inFlight.path = ""
			}
		}
		os.Exit(130)
	}()
}

// inFlight is the file currently mutated, for the signal guard.
var inFlight struct {
	path string
}

// anchorFor finds the anchor in `body`, tolerating line endings, and returns the
// form that matched.
//
// LINE ENDINGS SILENTLY KILLED FOUR MUTATIONS.
//
// The anchors are Go string literals, so they contain `\n`. A file checked out on
// Windows with `core.autocrlf=true` contains `\r\n`. Every SINGLE-line anchor still
// matched, and every MULTI-line anchor did not -- with no error, because the
// checker's own "does not apply" path was a `continue` that used to print nothing
// and then failed to be counted (3b9ce88).
//
// It surfaced here as "4 mutations never applied" on files that were demonstrably
// clean: the anchor byte count matched, every line of the anchor was present in the
// file, and only the joined form was absent. Nothing in the message said CRLF, and
// it was my own `git checkout` -- run to undo a corrupted file -- that rewrote
// payment_service.go from LF to CRLF and took M16, M25, M28 and M38 down with it.
//
// A harness that breaks on the platform's line endings is not portable, and the fix
// is not "normalise the file": the file is fine, the comparison was wrong.
func anchorFor(body, old string) (string, bool) {
	if strings.Contains(body, old) {
		return old, true
	}
	if !strings.Contains(body, "\r\n") {
		return old, false
	}
	crlf := strings.ReplaceAll(old, "\n", "\r\n")
	if strings.Contains(body, crlf) {
		return crlf, true
	}
	return old, false
}

// lineEndings describes a file's line endings for a diagnostic.
func lineEndings(body string) string {
	crlf := strings.Count(body, "\r\n")
	bare := strings.Count(body, "\n") - crlf
	switch {
	case crlf > 0 && bare == 0:
		return fmt.Sprintf("CRLF (%d)", crlf)
	case bare > 0 && crlf == 0:
		return fmt.Sprintf("LF (%d)", bare)
	default:
		return fmt.Sprintf("MIXED (crlf=%d lf=%d)", crlf, bare)
	}
}

// proveBaseline runs the test suite once, unmutated.
//
// Without this the checker cannot tell "no test noticed" from "no test ran". That is
// the same confusion as the skipped-mutation bug in 3b9ce88 arriving by a different
// route: there the tests never ran; here they ran and ALL of them failed, and the
// result was still reported as forty-eight mutations with no coverage.
func proveBaseline(pkg string) error {
	args := append([]string{"test"}, strings.Fields(pkg)...)
	args = append(args, "-count=1")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	return fmt.Errorf("go test %s failed on the UNMUTATED tree:\n%s",
		strings.Join(strings.Fields(pkg), " "), string(out))
}

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
		{
			// The claim is what makes replay a database guarantee. Remove the
			// conflict handling and it becomes an ordinary INSERT, which is the
			// check-then-write race it replaced: two identical webhooks arriving
			// together both write, and the buyer is refunded twice.
			label: "M21: the claim conflicts instead of deferring to the winner",
			file:  repoFile,
			old:   "\t\tON CONFLICT (gateway, gateway_ref) WHERE gateway_ref IS NOT NULL\n\t\tDO NOTHING\n",
			new:   "",
		},
		{
			// Postgres requires a partial index's predicate in the conflict target.
			// Omit it and the INSERT does not deduplicate at all -- the statement
			// still succeeds, still returns a row, and every replay writes another
			// one. It looks exactly like a working guard and is no guard at all.
			label: "M22: the claim does not target the partial unique index",
			file:  repoFile,
			old:   "\t\tON CONFLICT (gateway, gateway_ref) WHERE gateway_ref IS NOT NULL\n",
			new:   "\t\tON CONFLICT (gateway, gateway_ref)\n",
		},
		{
			// A racer that loses must be told it lost. Inverting the check means the
			// winner returns early and the loser goes on to settle the refund on
			// top of it -- the exact outcome the index exists to prevent, and the
			// database now holds only one row, so nothing downstream can detect it.
			//
			// Inverted rather than disabled: `if false` leaves `wasClaimed` unused,
			// which does not compile, and a mutation that does not compile is
			// reported as missed while testing nothing at all.
			label: "M23: a racer that lost the claim settles the refund anyway",
			file:  svcRefund,
			old:   "\tif !wasClaimed {",
			new:   "\tif wasClaimed {",
		},
		{
			// The hold's whole purpose. `WalletHeldTxOn` exists to move value out of
			// the balance a withdrawal reads, and this is the sign that does it.
			// Without the debit the hold writes a row, the release job updates the
			// row, and the seller's money stays withdrawable throughout -- the fraud
			// control that controls nothing.
			label: "M24: a hold no longer debits the spendable balance",
			file:  repoFile,
			old:   "\t\t   SET balance      = balance      + CASE WHEN $2::varchar = 'hold' THEN -$3 ELSE $3 END,",
			new:   "\t\t   SET balance      = balance      + CASE WHEN $2::varchar = 'release' THEN -$3 ELSE $3 END,",
		},
		{
			// The release must mirror the hold. Moving money in the hold direction
			// debits the spendable balance again, which is a second withdrawal wearing
			// the name of an unblock.
			label: "M25: a release moves money in the hold direction",
			file:  svcFile,
			old: "\tif err := s.payments.WalletHeldTxOn(ctx, q, res.SellerID, \"release\",\n" +
				"\t\trepository.WalletReasonHoldRelease, res.Amount); err != nil {",
			new: "\tif err := s.payments.WalletHeldTxOn(ctx, q, res.SellerID, \"hold\",\n" +
				"\t\trepository.WalletReasonHoldRelease, res.Amount); err != nil {",
		},
		{
			// A failed journal must stop the money moving. `postLedger` logs and
			// returns, which is right for a movement that already committed and wrong
			// for one that has not -- which is every write in the hold. Mutated on
			// postLedgerStrict's own return rather than at the call site, because
			// swapping the call for the swallowing one does not compile: the `err`
			// binding disappears and the following `err != nil` has nothing to test.
			label: "M27: a failed journal no longer stops the money moving",
			file:  svcPostFile,
			old:   "\tif _, err := s.ledger.Post(ctx, q, spec); err != nil {\n\t\treturn err\n\t}\n\treturn nil",
			new:   "\tif _, err := s.ledger.Post(ctx, q, spec); err != nil {\n\t\tslog.Error(\"ledger posting failed\", \"error\", err.Error())\n\t}\n\treturn nil",
		},
		{
			// COD capture posts no journal at all. The buyer paid cash to a
			// courier, escrow_held was never debited, and cod_receivable sat at zero
			// forever -- so the books showed Rp0 of captured COD against Rp100,000 of
			// goods delivered. The helper that routes COD correctly was never called
			// with a COD method, which is why the test that pinned the helper passed
			// while the path stayed broken.
			//
			// The whole block is removed rather than disabled: wrapping it in `if
			// false` leaves the `err` binding dangling and does not compile, and a
			// mutation that does not compile tests nothing.
			label: "M28: COD capture collects the cash without booking it",
			file:  svcFile,
			old: "\tif err := s.postLedgerStrict(ctx, tx.Querier(), JournalSpec{\n" +
				"\t\tIdempotencyKey: \"capture:\" + intent.ID,\n" +
				"\t\tTxType:         TxTypePayment,\n" +
				"\t\tRefType:        \"payment_intent\",\n" +
				"\t\tRefID:          intent.ID,\n" +
				"\t\tNote:           \"COD collected on delivery; funds held in escrow\",\n" +
				"\t\tEntries: withMeta(captureEntries(intent.Amount, intent.Method), map[string]any{\n" +
				"\t\t\t\"order_id\": intent.OrderID, \"method\": intent.Method,\n" +
				"\t\t}),\n" +
				"\t}); err != nil {\n\t\treturn err\n\t}\n",
			new: "",
		},
		{
			// The COD hold is the only one that bites where the money becomes
			// withdrawable. Removing it leaves a COD seller free to withdraw money a
			// refused delivery will reverse, and the refund then fails on
			// `balance >= amount` with the buyer's money stranded.
			label: "M29: escrow release credits a COD seller with no hold",
			file:  svcFile,
			old:   "\tif intent.Method == domain.MethodCOD && sellerAmount > 0 {",
			new:   "\tif false && intent.Method == domain.MethodCOD && sellerAmount > 0 {",
		},
		{
			// The fix for 0cb43d1's incomplete half. `ledger_entries.account_code`
			// and `account_balances.account_code` both FK to `ledger_accounts(code)`,
			// so a posting to a seller who has no account row fails on the insert --
			// and `postLedger` swallows that. This mutation is what the defect WAS:
			// escrow release, every payout transition and every post-release refund
			// moving money with no journal.
			label: "M30: a posting does not ensure the personal accounts it needs",
			file:  svcPostFile,
			old:   "\tif err := s.ensureJournalAccounts(ctx, q, spec.Entries); err != nil {\n\t\tslog.Error(\"ledger account missing; the money movement is not accounted for\",",
			new:   "\tif err := error(nil); err != nil {\n\t\tslog.Error(\"ledger account missing; the money movement is not accounted for\",",
		},
		{
			// And the swallow half of it: with the ensure in place, a failure must
			// still stop the posting rather than passing silently.
			label: "M31: a failed account ensure is swallowed on the strict path",
			file:  svcPostFile,
			old:   "\tif err := s.ensureJournalAccounts(ctx, q, spec.Entries); err != nil {\n\t\treturn err\n\t}",
			new:   "\tif err := s.ensureJournalAccounts(ctx, q, spec.Entries); err != nil {\n\t\tslog.Error(\"ensure failed\", \"error\", err.Error())\n\t}",
		},
		{
			// A single scalar applied the platform default to every seller, which is
			// what made an override impossible. Reverting to it is not a subtle bug:
			// every seller's configured lag is silently ignored.
			label: "M32: one lag scalar is applied to every seller",
			file:  repoFile,
			old:   "\t\t   AND sr.created_at <= now() - make_interval(\n\t\t           days => COALESCE(st.payout_lag_days, $1))",
			new:   "\t\t   AND sr.created_at <= now() - make_interval(days => $1)",
		},
		{
			// An INNER JOIN excludes every seller with no store row, which fails
			// SAFE but SILENTLY: no hold is ever released, the queue just grows, and
			// nothing reports it. `LEFT JOIN` is what makes the fallback reachable.
			label: "M33: a seller with no store row can never be released",
			file:  repoFile,
			old:   "\t\t  LEFT JOIN stores st ON st.owner_id = sr.seller_id",
			new:   "\t\t  JOIN stores st ON st.owner_id = sr.seller_id",
		},
		{
			// The store is resolved before the write because the route carries a
			// store id and the column is keyed on owner_id. Without the resolution
			// the UPDATE matches zero rows and reports success -- an operator believes
			// they set a payout term that was never stored.
			label: "M34: the payout lag is written without resolving the store",
			file:  sellerFile,
			old:   "\treturn s.stores.SetPayoutLagDays(ctx, store.OwnerID, days)",
			new:   "\t_ = store\n\treturn s.stores.SetPayoutLagDays(ctx, storeID, days)",
		},
		{
			// A batch that claims its rows out of `pending` makes them un-settleable
			// AND un-failable at once, because PayoutForUpdate, MarkPayoutSentTx and
			// MarkPayoutFailedTx all require status = 'pending'. Their
			// seller_pending balance is credited on request and debited only by the
			// two settlement helpers, so it would be stranded with no path back and
			// no repair anywhere in the codebase.
			label: "M35: the batch claim let a payout already sit in a live batch",
			file:  repoFile,
			old:   "AND NOT EXISTS (\n\t\t       SELECT 1 FROM payout_batch_items i",
			new:   "AND true OR NOT EXISTS (\n\t\t\t       SELECT 1 FROM payout_batch_items i",
		},
		{
			// A CANCELLED batch must release its withdrawals. Treating it as live
			// strands every payout in it: it can never be batched again, so the money
			// is withdrawable by the seller but never payable by the operator. Safe,
			// and permanent.
			label: "M36: a cancelled batch still held its claim on the payouts",
			file:  repoFile,
			old:   "\t\t          AND b.status <> 'cancelled'",
			new:   "\t\t          AND b.status IS NOT NULL",
		},
		{
			// Without ON CONFLICT, a retried daily run creates a SECOND batch for
			// the same cutoff and groups the same withdrawals into it -- two remittance
			// files, two bank runs, one payment paid twice. This is the
			// settlement_imports lesson from ff15503, restated in 00044:27-34.
			label: "M37: the batch run was not idempotent, so a retry paid twice",
			file:  repoFile,
			old:   "ON CONFLICT (batch_ref) WHERE batch_ref IS NOT NULL DO NOTHING",
			new:   "ON CONFLICT DO NOTHING",
		},
		{
			// An empty batch is not approvable: the record would claim an operator
			// reviewed a file with no rows in it.
			label: "M38: an empty batch could be approved as though it were reviewed",
			file:  svcFile,
			old:   "\tif items == 0 {\n\t\treturn 0, domain.E(domain.KindConflict, \"PAYOUT_BATCH_EMPTY\"",
			new:   "\tif false {\n\t\treturn 0, domain.E(domain.KindConflict, \"PAYOUT_BATCH_EMPTY\"",
		},
		{
			// The remittance file IS the instruction to move money. Generated from a
			// draft, the daily unattended batch run would produce a payment
			// instruction nobody looked at, and the approval would be the only thing
			// that could ever stop it.
			label: "M39: an unapproved batch produced a payment instruction",
			file:  svcFile,
			old:   "\tif batch.Status != repository.PayoutBatchApproved &&",
			new:   "\tif false && batch.Status != repository.PayoutBatchApproved &&",
		},
		{
			// An incremental counter and the rows disagree after a retried run, and
			// the operator approves the number. Deriving it from the rows means the
			// two cannot drift.
			label: "M40: the batch total was incremented rather than recounted",
			file:  repoFile,
			old:   "\t\t   SET total = COALESCE(agg.sum, 0),",
			new:   "\t\t   SET total = COALESCE(agg.sum, 0) + 1,",
		},
		{
			// Removing partially_shipped from the packed row makes every split
			// shipment fail at order_service.go:918 with a transition error, while
			// 00050's tables sit there holding nothing -- the same shape as the two
			// dead things already found here (00043's payout batches, and
			// TaskReleasePayoutReservations).
			label: "M41: the state machine refused every split shipment",
			file:  domainFile,
			old:   "\tOrderPacked:  {OrderPartiallyShipped, OrderShipped, OrderCancelled},",
			new:   "\tOrderPacked:  {OrderShipped, OrderCancelled},",
		},
		{
			// The second parcel of a split shipment could never be recorded, so a
			// two-parcel order would stay partially_shipped for ever.
			label: "M42: a half-shipped order could never finish shipping",
			file:  domainFile,
			old:   "\tOrderPartiallyShipped: {OrderShipped, OrderDelivered},",
			new:   "\tOrderPartiallyShipped: {},",
		},
		{
			// Allowing cancel from a half-shipped order tells a buyer their order is
			// off while parcels are physically gone and paid for, and strands the
			// shipped lines with no path back.
			label: "M43: a half-shipped order could be cancelled",
			file:  domainFile,
			old:   "\tOrderPartiallyShipped: {OrderShipped, OrderDelivered},",
			new:   "\tOrderPartiallyShipped: {OrderShipped, OrderDelivered, OrderCancelled},",
		},
		{
			// Over-shipping is the defect the whole shipped_quantity column exists to
			// prevent. The CHECK on order_items is the backstop; this predicate is the
			// mechanism, because the CHECK only fires when the item row is written and
			// two concurrent parcels would each pass it.
			label: "M44: a parcel could claim more units than were bought",
			file:  shipFile,
			old:   "AND shipped_quantity + $2 <= quantity",
			new:   "AND shipped_quantity + $2 >= 0",
		},
		{
			// THE SPLIT-SHIPMENT DEADLOCK. Recording the first parcel of a two-parcel
			// order moves the order to partially_shipped; the shortcut then refuses
			// everything that is not `packed`, so the second parcel can never be
			// recorded. The seller is left with half an order and no way to finish it.
			label: "M45: a split order could never finish shipping",
			file:  sellerFile,
			old:   "if order.Status == domain.OrderPartiallyShipped {",
			new:   "if false && order.Status == domain.OrderPartiallyShipped {",
		},
		{
			// Cancelling a half-shipped order tells a buyer their order is off while
			// parcels are physically gone and paid for. (M43 covers the state machine;
			// this is the seller endpoint that reaches it.)
			label: "M46: a seller could cancel an order that is already in a box",
			file:  shipSvcFile,
			old:   "order.Status == domain.OrderCancelled || order.Status == domain.OrderReturned",
			new:   "false",
		},
		{
			// The parcel exists but the order still says packed. Every reader of the
			// order -- the buyer page, the seller list, admin search -- believes nothing
			// has shipped while a parcel says otherwise.
			label: "M47: the order status was no longer derived from the parcels",
			file:  shipSvcFile,
			old:   "\tif err := s.applyDerivedStatus(ctx, q, orderID, order.Status); err != nil {",
			new:   "\tif err := error(nil); err != nil {",
		},
		{
			// Notifying the buyer on the FIRST parcel of two tells them their order is
			// on its way when most of it is still in a warehouse.
			label: "M48: the buyer was told the order shipped while half of it was still in a warehouse",
			file:  sellerFile,
			// `if true {` was the first version and it does NOT compile: `allShipped`
			// becomes declared and not used, the package fails to build, and the
			// checker correctly refuses to count a build failure as coverage -- which
			// it reported as "the mutation did not compile, so no test ran".
			//
			// The tautology below always fires while still USING the variable, which
			// is what makes this a real mutation rather than a build error.
			old: "\tif allShipped {",
			new: "\tif allShipped || !allShipped {",
		},
		{
			// Cancelled and returned lines must be excluded from the derivation. Count
			// them and an order of three units with one cancelled can never reach
			// "shipped" -- every retry re-derives the same wrong answer.
			label: "M49: cancelled lines counted towards the shipped total, so an order could never complete",
			file:  orderRepoFile,
			// The anchor is the TAIL of the DeriveShippingStatus query, not the bare
			// predicate: the same predicate also appears in SyncOrderItemStatuses, and
			// Replace(..., 1) mutates the FIRST match. An anchor that appears twice
			// silently checks the wrong method -- this one reported M49 SURVIVED while
			// editing code no assertion reads.
			old: "COUNT(*) FILTER (WHERE shipped_quantity < quantity)\n" +
				"\t\t  FROM order_items\n\t\t WHERE order_id = $1::uuid\n" +
				"\t\t   AND status NOT IN ('cancelled', 'returned')`, orderID).",
			new: "COUNT(*) FILTER (WHERE shipped_quantity < quantity)\n" +
				"\t\t  FROM order_items\n\t\t WHERE order_id = $1::uuid\n" +
				"\t\t   AND true`, orderID).",
		},
		{
			// A derivation the machine refuses must be reported. Silently ignoring it
			// leaves the parcel recorded and the status lying.
			label: "M50: an illegal derived status was forced through",
			file:  shipSvcFile,
			old:   "\tif !domain.CanTransition(current, derived) {",
			new:   "\tif false {",
		},
		{
			// A registry that answers an unknown carrier with a made-up one is the
			// defect this package exists to avoid: a seller asking for "jnE" gets a
			// self-minted tracking number, believes a courier is involved, and spends
			// three days waiting on one. This is why it is a map and not a switch with
			// a default branch.
			label: "M51: an unconfigured carrier was answered with a made-up one",
			file:  carrierFile,
			old:   "\tkey = ManualCarrierName",
			new:   "\tkey = \"jne\"",
		},
		{
			// `attempted` is a MISSED delivery, which usually re-delivers. A terminal
			// mapping leaves the parcel stuck for ever, because MarkShipmentDelivered
			// correctly refuses a terminal status.
			label: "M52: a missed delivery was mapped to a terminal shipment state",
			file:  carrierFile,
			old:   "\t\treturn \"exception\", true",
			new:   "\t\treturn \"cancelled\", true",
		},
		{
			// An unknown carrier state must leave the parcel alone. Guessing from a
			// provider's renamed field is how a parcel gets marked cancelled.
			label: "M53: an unrecognised carrier state was translated into a guess",
			file:  carrierFile,
			old:   "\t\treturn \"\", false",
			new:   "\t\treturn \"in_transit\", true",
		},
		{
			// Buying a label spends real money. A second label for one box leaves the
			// seller with two paid labels and no way to tell which is live.
			label: "M54: a label retry bought a second label for the same parcel",
			file:  manualFile,
			old:   "\tif existing, ok := m.handed[in.ParcelID]; ok {",
			new:   "\tif existing, ok := m.handed[in.ParcelID]; ok && false {",
		},
		{
			// The manual carrier must not invent movement. A reconciled in_transit for
			// a parcel nobody is watching is a lie stored as fact.
			label: "M55: the manual carrier claimed movement it cannot know about",
			file:  manualFile,
			old:   "\t\tStatus:   TrackingPending,",
			new:   "\t\tStatus:   TrackingInTransit,",
		},
		{
			// This refusal happens before any money is spent, which is what makes it
			// cheap. A label addressed nowhere is money gone and a parcel nobody
			// receives.
			label: "M56: a label was bought for a parcel with no destination",
			file:  manualFile,
			old:   "\tif in.Destination.IsZero() {",
			new:   "\tif false {",
		},
		{
			// Most orders are one box. Losing the shortcut forces a seller to name
			// the contents of an order going in a single parcel, for no information.
			label: "M57: the single-parcel transition route was dropped",
			file:  routerFile,
			old:   `r.Post("/orders/{id}/transition", seller.FulfillOrder)`,
			new:   `r.Post("/orders/{id}/legacy-transition", seller.FulfillOrder)`,
		},
		{
			// The idempotency key must be the parcel, not the attempt.
			label: "M58: a label retry was given a fresh idempotency key",
			file:  shipSvcFile,
			old:   "\t\tIdempotency: shipment.ID,",
			new:   "\t\tIdempotency: shipment.ID + \"-retry\",",
		},
		{
			// A delivered parcel must not get a new label: money spent on a box that
			// is not going anywhere.
			label: "M59: money was spent on a label for an already-delivered parcel",
			file:  shipSvcFile,
			old:   "\tif shipment.DeliveredAt != nil {",
			new:   "\tif false {",
		},
		{
			// THE ONE THAT WAS WRITTEN AND CAUGHT IN THE SAME SITTING.
			//
			// returnDestinationFor takes a sellerID; the call site passed
			// shipment.OrderID. Both are strings, so THE COMPILER WAS HAPPY. The lookup
			// is `WHERE owner_id = $1`, an order id never matches a store owner, so every
			// return label is refused with "no destination" and the whole return feature
			// is dead -- while every carrier test still passes, because the carrier is
			// behaving perfectly on an empty address.
			label: "M64: the return label was addressed to the order, so no return label could ever be bought",
			file:  shipSvcFile,
			old:   "returnDestinationFor(ctx, shipment.SellerID)",
			new:   "returnDestinationFor(ctx, shipment.OrderID)",
		},
		{
			// shipped_quantity is the over-shipment counter, and
			// DeriveShippingStatus compares it against quantity to decide partially_shipped
			// vs shipped. Counting a return makes a delivered order re-assert `shipped`
			// and counts one unit twice: once out, once back.
			label: "M60: a return parcel reserved shipped units, as if it were a new outbound shipment",
			file:  returnRepoFile,
			old:   "INSERT INTO shipment_items (shipment_id, order_item_id, quantity)",
			new: "INSERT INTO shipment_items (shipment_id, order_item_id, quantity) " +
				"-- and UPDATE order_items SET shipped_quantity = quantity",
		},
		{
			// A `requested` return is one the seller has not agreed to. A parcel for it is
			// goods in motion that nobody has accepted responsibility for.
			label: "M61: an unapproved return could be sent back",
			file:  shipSvcFile,
			old:   "if status != domain.ReturnApproved {",
			new:   "if false {",
		},
		{
			// Two boxes arriving against one approved refund means one matches nothing
			// and the seller decides by eye.
			label: "M62: a return could have a second parcel",
			file:  returnRepoFile,
			old:   "if err == nil {\n\t\treturn nil, ErrReturnAlreadyParcelled\n\t}",
			new:   "if false {\n\t\treturn nil, ErrReturnAlreadyParcelled\n\t}",
		},
		{
			// A return marked returned with the box still in a depot is a refund paid for
			// goods nobody has. The guard must be the PARCEL's delivery, not the seller's
			// word -- and a seller clicking "it arrived" is not evidence.
			label: "M63: a return could be marked returned before its parcel arrived",
			file:  returnRepoFile,
			old:   "AND s.status = 'delivered'",
			new:   "AND s.status <> 'cancelled'",
		},
		{
			// The seller owning the pickup address is the fraud control on the whole
			// feature: a seller who can create and dispatch a return parcel marks goods
			// as returned without them ever leaving the buyer, and the refund is real.
			// The buyer-ownership check on the return parcel path.
			//
			// The first version of this mutation rewrote a function signature to invent
			// a seller-side parcel creator, and it DID NOT COMPILE. The checker correctly
			// refused to count a build failure as coverage and reported "the mutation did
			// not compile, so no test ran" -- which is the right answer and still a hole
			// in the evidence.
			//
			// The property that actually matters is narrower, and it is the one a
			// careless caller or a future refactor would break: the check that the
			// return belongs to the caller. Disable it and ANY party can create a return
			// parcel for ANY return -- which is the fraud control on this whole feature,
			// because a caller who can mark goods as returned without them ever leaving
			// the buyer gets a real refund.
			// `false &&` rather than plain `false`: a bare `if false` leaves `owner`
			// declared and unused, the package fails to build, and the checker refuses
			// to count a build failure as coverage. That is the right behaviour and still
			// a hole in the evidence, so the mutation has to compile -- which is also
			// how this bug actually appears in practice: a guard "temporarily" relaxed
			// with a `&& false`.
			label: "M65: the return-ownership check was disabled, so any caller could send a return back",
			file:  orderSvcFile,
			old:   "\tif owner != buyerID {",
			new:   "\tif false && owner != buyerID {",
		},
		{
			// The journey lives on the parcel. A return row claiming `in_transit` would
			// duplicate it in a second table, which is the same disagreement 00050 had to
			// fix for the order status, one level down.
			label: "M66: a new in_transit status was added to the return row",
			file:  returnRepoFile,
			old:   "SET status = 'returned', updated_at = now()",
			new:   "SET status = 'in_transit', updated_at = now()",
		},
	}

	// skipped is counted SEPARATELY from missed, and both are fatal. A skip is
	// "never checked", which is a different and worse failure than "checked and
	// not caught" -- and it used to print SKIP, then `continue` without touching
	// `missed`, so the run ended with "0 missed", exited 0, and read as success.
	//
	// That is not hypothetical: M35 was skipped at the moment it was added because
	// its anchor was off by one tab, and 2814c52 shipped claiming "39/39 mutations
	// caught". It was 38 of 39. The batch claim -- the NOT EXISTS that stops a
	// payout being in two remittance files -- had no coverage at all.
	recoverInterrupted(mutatedFiles(muts))
	installSignalGuard()

	// A BROKEN BASELINE MUST ABORT, NOT REPORT.
	//
	// Every result below is the DIFFERENCE between "the tests pass" and "the tests
	// fail with this one change". With a baseline that does not build, that
	// difference is meaningless -- and the checker does not look meaningless.
	//
	// A full C: drive made every package fail to build, and the run reported
	//
	//     1 caught, 48 missed, 0 never checked, 49 total
	//
	// which reads exactly like "48 mutations have no coverage" -- the precise alarm
	// this tool exists to raise, pointed at nothing. Each was classified as a compile
	// error in the mutation, which it was not: the BASELINE did not compile.
	//
	// So the baseline is proved once, first, and a failure aborts with a non-zero exit
	// and no per-mutation table at all.
	if err := proveBaseline(allPkgs); err != nil {
		fmt.Printf("ABORT: the baseline does not pass, so no mutation result would mean "+
			"anything.\n"+
			"       Every result is a comparison against this run, and with no clean "+
			"run to compare against the whole table is noise.\n\n%s\n", indent(err.Error()))
		os.Exit(2)
	}

	mutationCount := 0
	caught, missed, skipped := 0, 0, 0
	for _, m := range muts {
		orig, err := os.ReadFile(m.file)
		if err != nil {
			skipped++
			fmt.Printf("FAIL %s\n       NOT CHECKED -- %v\n", m.label, err)
			continue
		}
		body := string(orig)
		pat, anchored := anchorFor(body, m.old)
		if !anchored {
			skipped++
			fmt.Printf("FAIL %s\n       NOT CHECKED -- the anchor does not appear in %s, so "+
				"this mutation was never applied and proves nothing either way. An anchor "+
				"one tab out of place is how a mutation sits unrun while the tally reads "+
				"clean.\n", m.label, m.file)
			// Print the anchor. Without it this failure is a guessing game: the
			// message says the text is absent and gives no way to see which text.
			// Four anchors drifted during F2 and the only way to find them was to
			// read each one out of this file by hand.
			fmt.Printf("       anchor (%d bytes): %q\n", len(m.old), truncate(m.old, 220))
			fmt.Printf("       looking in      : %q\n", truncate(m.file, 220))
			// The line endings. This turned out to be the cause of every one of these
			// failures, and nothing else in the message hinted at it.
			fmt.Printf("       file line ends  : %s\n", lineEndings(body))
			// Say WHICH LINE diverges. Four multi-line anchors failed during F2 with
			// a message that said only "not found", and each one turned out to be
			// present when checked by hand -- so the byte count matched, the file was
			// clean, and there was nothing to act on. Reporting the first line that
			// is absent makes the failure actionable instead of a guessing game.
			for i, line := range strings.Split(m.old, "\n") {
				if line == "" {
					continue
				}
				if !strings.Contains(body, line) {
					fmt.Printf("       line %d absent : %q\n", i+1, truncate(line, 160))
				}
			}
			continue
		}
		// `pat` is `m.old` in the FILE's line endings, and `repl` is `m.new` in the
		// same. Applying an LF anchor to a CRLF file would write LF lines into a CRLF
		// file, which gofmt then rewrites and which makes the diff unreadable.
		repl := m.new
		if pat != m.old {
			repl = strings.ReplaceAll(m.new, "\n", "\r\n")
		}
		mutated := strings.Replace(body, pat, repl, 1)
		// Back the original up to DISK before mutating. The defer below restores on
		// a normal return; this survives being killed.
		if err := stage(m.file, orig); err != nil {
			skipped++
			fmt.Printf("FAIL %s\n       NOT CHECKED -- could not back the file up: %v\n",
				m.label, err)
			continue
		}
		if err := os.WriteFile(m.file, []byte(mutated), 0o644); err != nil {
			skipped++
			_ = stage(m.file, orig)
			fmt.Printf("FAIL %s\n       NOT CHECKED -- %v\n", m.label, err)
			continue
		}
		inFlight.path = m.file
		// Restore on EVERY exit path, including a panic. Without this, a
		// Ctrl-C mid-run leaves a mutated source file in the working tree and the
		// next test run fails for a reason that has nothing to do with the code --
		// which happened during development and cost a confused debugging detour.
		// Belt: restore on every normal exit path from here on. The braces are
		// deliberate -- `defer` inside a loop body registers on the FUNCTION, so it
		// does not fire per iteration, and every earlier version of this comment was
		// quietly wrong about that.
		restore := func() {
			_ = os.WriteFile(m.file, orig, 0o644)
			unstage(m.file)
			inFlight.path = ""
		}
		defer restore()

		pkg := allPkgs
		// Fields, not pkg: a single argument containing a space is one malformed
		// package path, and `go test` answers that with "[setup failed]" -- a line
		// containing "FAIL" and no test in it. See the file header.
		args := append([]string{"test"}, strings.Fields(pkg)...)
		args = append(args, "-count=1")
		cmd := exec.Command("go", args...)
		out, _ := cmd.CombinedOutput()
		// The build cache grows by roughly a full rebuild per mutation, because each
		// mutation invalidates every package that depends on the mutated file. Fifty
		// mutations filled 11 GB of C:, and the next thing that failed was the disk --
		// a failure that looks exactly like a code problem.
		//
		// Cleaned every few mutations so the ceiling is a few GB rather than the whole
		// drive. `go clean -cache` is cheap next to a run that fails for no reason.
		mutationCount++
		if mutationCount%6 == 0 {
			_ = exec.Command("go", "clean", "-cache").Run()
		}
		// Restore immediately rather than waiting for the deferred copy: the window
		// between a mutation being written and being restored is exactly the window
		// in which a killed process leaves the tree broken.
		restore()

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

	// The counts are printed together and the skipped one is spelled out, because
	// "38 caught, 0 missed, 39 total" invites the reader to do 38+0=39 and conclude
	// everything passed. It did not: one was never run.
	fmt.Printf("\n%d caught, %d missed, %d never checked, %d total\n",
		caught, missed, skipped, len(muts))
	if skipped > 0 {
		fmt.Printf("FAIL: %d mutation(s) were never applied. An unapplied mutation is "+
			"not a passing mutation; its anchor has drifted and needs fixing.\n", skipped)
	}
	if missed > 0 || skipped > 0 {
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

// mutatedFiles lists every file any mutation targets.
//
// recoverInterrupted is given this list so a stale backup for a file no longer under
// mutation is not silently ignored, and so a backup with no corresponding mutation
// can be reported rather than left to rot.
func mutatedFiles(list []mutation) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.file)
	}
	return out
}

// truncate shortens a string for display, marking that it was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
