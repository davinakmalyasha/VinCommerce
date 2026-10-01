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
	svcFile    = "internal/service/payment_service.go"
	svcRefund  = "internal/service/refund_service.go"
	sellerFile = "internal/service/seller_service.go"
	// svcPostFile holds the journal-posting helpers. Mutated separately because the
	// distinction between the swallowing `postLedger` and the strict
	// `postLedgerStrict` IS the guard, and a mutation that swaps a call site cannot
	// express it without failing to compile.
	svcPostFile = "internal/service/ledger_postings.go"
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
			old:   "AND NOT EXISTS (\n\t\t\t       SELECT 1 FROM payout_batch_items i",
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
